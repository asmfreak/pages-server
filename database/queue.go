package database

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	gq "github.com/golang-queue/queue"
	gqc "github.com/golang-queue/queue/core"
	"github.com/philippgille/gokv/encoding"
	"github.com/philippgille/gokv/syncmap"
)

type Params struct {
	Filename string `cli:"usage:'path to database',default:'pages-server.db'"`
}

type dedupValue struct {
	Running bool
	Posted  time.Time
}

type queueState struct {
	queue *gq.Queue
	dedup Store[string, dedupValue]
}

type Queue struct {
	queues map[string]*queueState
}

func (q *Queue) Close() error {
	for _, q := range q.queues {
		q.queue.Release()
	}
	return nil
}

var queueCtxKey = ctxKey{"liteq"}

func QueueFromContext(ctx context.Context) *Queue {
	q := ctx.Value(queueCtxKey)
	if q == nil {
		return nil
	}
	return q.(*Queue)
}

type jobT struct {
	Posted time.Time
	Job    string
}

func (j *jobT) Bytes() []byte {
	b, err := json.Marshal(j)
	if err != nil {
		panic(err)
	}
	return b
}

func NewQueue(_ context.Context, tasks ...Task) (*Queue, error) {
	ret := &Queue{
		queues: make(map[string]*queueState),
	}

	for _, task := range tasks {
		qn := task.TaskElement().QueueName()
		qs := &queueState{
			queue: nil,
			dedup: &store[string, dedupValue]{
				syncmap.NewStore(syncmap.Options{
					Codec: encoding.JSON,
				}),
			},
		}
		qs.queue = gq.NewPool(2, gq.WithFn(func(ctx context.Context, msg gqc.QueuedMessage) error {
			ctx = context.WithValue(ctx, queueCtxKey, ret)
			job, ok := msg.(*jobT)
			if !ok {
				if err := json.Unmarshal(msg.Bytes(), &job); err != nil {
					return err
				}
			}
			slog.Info("starting consumer", "task", qn)
			te := task.TaskElement()
			err := te.ParseJob(job.Job)
			if err != nil {
				slog.Error("failed to parse job", "task", qn, "job", job.Job, "err", err)
				return fmt.Errorf("failed to parse job %w", err)
			}
			dedupState, ok, _ := qs.dedup.Get(te.DedupingKey())
			if ok {
				if job.Posted.Before(dedupState.Posted) {
					slog.Info("skipping job - task is older than the last run", "task", qn, "job", job.Job, "jobPosted", job.Posted, "another job is running", dedupState.Posted)
					return nil
				}
				if dedupState.Running {
					slog.Info("skipping job - another job is still running", "task", qn, "job", job.Job)
					return nil
				}
			}
			_ = qs.dedup.Set(te.DedupingKey(), dedupValue{Posted: time.Now(), Running: true})
			slog.Info("starting consumer runner", "task", qn, "job", te, "err", err)
			err = task.Runner(ctx, te)
			if err != nil {
				_ = qs.dedup.Delete(te.DedupingKey())
				slog.Error("task failed", "task", qn, "job", job.Job, "err", err)
				return err
			}
			slog.Info("task finished", "task", qn, "job", job.Job)
			_ = qs.dedup.Set(te.DedupingKey(), dedupValue{Posted: time.Now(), Running: false})
			return nil
		}))
		ret.queues[qn] = qs
	}
	return ret, nil
}

type Task interface {
	// Consumer function
	Runner(ctx context.Context, task TaskElement) error
	TaskElement() TaskElement
}

type TaskElement interface {
	QueueName() string
	ParseJob(string) error
	Job() string
	DedupingKey() string
}

type TaskElementPtr[T any] interface {
	*T
	TaskElement
}

type funcTask[T any, PT TaskElementPtr[T]] struct {
	f func(ctx context.Context, task PT) error
}

func (t *funcTask[T, PT]) Runner(ctx context.Context, task TaskElement) error {
	return t.f(ctx, task.(PT))
}

func (t *funcTask[T, PT]) TaskElement() TaskElement {
	var te T
	var pt PT = &te
	return pt
}

func FuncTask[T any, PT TaskElementPtr[T]](f func(ctx context.Context, task PT) error) Task {
	return &funcTask[T, PT]{f: f}
}

func (q *Queue) Enqueue(_ context.Context, t TaskElement) error {
	return q.queues[t.QueueName()].queue.Queue(&jobT{
		Posted: time.Now(),
		Job:    t.Job(),
	})
}
