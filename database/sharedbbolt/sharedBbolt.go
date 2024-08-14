package sharedbbolt

import (
	"errors"
	"sync"
	"sync/atomic"

	bolt "go.etcd.io/bbolt"

	"github.com/philippgille/gokv/encoding"
	"github.com/philippgille/gokv/util"
)

type SharedState struct {
	p       atomic.Pointer[bolt.DB]
	buckets map[string]struct{}
	pl      sync.Mutex
}

func (s *SharedState) Get(bucketName, key []byte, value *[]byte) (bool, error) {
	db := s.p.Load()
	if db == nil {
		return false, errors.New("db is not initialized")
	}
	err := db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketName)
		txData := b.Get(key)
		// txData is only valid during the transaction.
		// Its value must be copied to make it valid outside of the tx.
		// TODO: Benchmark if it's faster to copy + close tx,
		// or to keep the tx open until unmarshalling is done.
		if txData != nil {
			// `data = append([]byte{}, txData...)` would also work, but the following is more explicit
			*value = make([]byte, len(txData))
			copy(*value, txData)
		}
		return nil
	})
	if err != nil {
		return false, nil
	}
	return true, nil
}

func (s *SharedState) Set(bucketName, key, value []byte) error {
	db := s.p.Load()
	if db == nil {
		return errors.New("db is not initialized")
	}
	return db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketName)
		return b.Put(key, value)
	})
}

func (s *SharedState) Delete(bucketName, key []byte) error {
	db := s.p.Load()
	if db == nil {
		return errors.New("db is not initialized")
	}
	return db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketName)
		return b.Delete(key)
	})
}

func (s *SharedState) Close(bucketName string) error {
	s.pl.Lock()
	defer s.pl.Unlock()
	delete(s.buckets, bucketName)
	if len(s.buckets) != 0 {
		return nil
	}
	db := s.p.Load()
	if db == nil {
		return nil
	}
	err := db.Close()
	if err != nil {
		return err
	}
	s.p.Store(nil)
	return nil
}

// Store is a gokv.Store implementation for bbolt (formerly known as Bolt / Bolt DB).
type Store struct {
	db         *SharedState
	bucketName []byte
	codec      encoding.Codec
}

// Set stores the given value for the given key.
// Values are automatically marshalled to JSON or gob (depending on the configuration).
// The key must not be "" and the value must not be nil.
func (s *Store) Set(k string, v any) error {
	if err := util.CheckKeyAndValue(k, v); err != nil {
		return err
	}

	// First turn the passed object into something that bbolt can handle
	data, err := s.codec.Marshal(v)
	if err != nil {
		return err
	}

	err = s.db.Set(s.bucketName, []byte(k), data)
	if err != nil {
		return err
	}
	return nil
}

// Get retrieves the stored value for the given key.
// You need to pass a pointer to the value, so in case of a struct
// the automatic unmarshalling can populate the fields of the object
// that v points to with the values of the retrieved object's values.
// If no value is found it returns (false, nil).
// The key must not be "" and the pointer must not be nil.
func (s *Store) Get(k string, v any) (found bool, err error) {
	err = util.CheckKeyAndValue(k, v)
	if err != nil {
		return false, err
	}
	var data []byte
	found, err = s.db.Get(s.bucketName, []byte(k), &data)
	if err != nil {
		return false, err
	}
	if !found {
		return false, nil
	}
	// If no value was found return false
	if data == nil {
		return false, nil
	}
	return true, s.codec.Unmarshal(data, v)
}

// Delete deletes the stored value for the given key.
// Deleting a non-existing key-value pair does NOT lead to an error.
// The key must not be "".
func (s *Store) Delete(k string) error {
	if err := util.CheckKey(k); err != nil {
		return err
	}

	return s.db.Delete(s.bucketName, []byte(k))
}

// Close closes the store.
// It must be called to make sure that all open transactions finish and to release all DB resources.
func (s *Store) Close() error {
	return s.db.Close(string(s.bucketName))
}

// Options are the options for the bbolt store.
type Options struct {
	// Bucket name for storing the key-value pairs.
	// Optional ("default" by default).
	BucketName string

	// Encoding format.
	// Optional (encoding.JSON by default).
	Codec encoding.Codec
}

// DefaultOptions is an Options object with default values.
// BucketName: "default", Path: "bbolt.db", Codec: encoding.JSON
var DefaultOptions = Options{
	BucketName: "default",
	Codec:      encoding.JSON,
}

// Path of the DB file.
// Optional ("bbolt.db" by default).
var DefaultPath = "bbolt.db"

func NewSharedState(path string) (*SharedState, error) {
	ret := &SharedState{
		buckets: make(map[string]struct{}),
	}
	if path == "" {
		path = DefaultPath
	}
	db, err := bolt.Open(path, 0o600, nil)
	if err != nil {
		return nil, err
	}
	ret.p.Store(db)
	return ret, nil
}

// NewStore creates a new bbolt store.
// Note: bbolt uses an exclusive write lock on the database file so it cannot be shared by multiple processes.
// So when creating multiple clients you should always use a new database file (by setting a different Path in the options).
//
// You must call the Close() method on the store when you're done working with it.
func (s *SharedState) NewStore(options Options) (*Store, error) {
	result := Store{}

	// Set default values
	if options.BucketName == "" {
		options.BucketName = DefaultOptions.BucketName
	}

	if options.Codec == nil {
		options.Codec = DefaultOptions.Codec
	}

	s.pl.Lock()
	defer s.pl.Unlock()

	db := s.p.Load()
	if db == nil {
		return nil, errors.New("db is not initialized")
	}

	if _, ok := s.buckets[options.BucketName]; ok {
		return nil, errors.New("bucket already exists and there should be only one bucket per store")
	}

	bucket := []byte(options.BucketName)
	// Create a bucket if it doesn't exist yet.
	// In bbolt key/value pairs are stored to and read from buckets.
	err := db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists(bucket)
		if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	s.buckets[options.BucketName] = struct{}{}
	result.db = s
	result.bucketName = bucket
	result.codec = options.Codec

	return &result, nil
}
