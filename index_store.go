package marcidus

import (
  "errors"
  "time"

  bolt "go.etcd.io/bbolt"
)

var (
  idxBucketName = []byte("entryIdByValue")

  errNotInitialized = errors.New("index not initialized")
  errUnknownEntry = errors.New("unknown entry")
)

type indexStore struct {
  db *bolt.DB
}

func newIndexStore(filePath string) (*indexStore, error) {
  db, err := bolt.Open(filePath, 0644, &bolt.Options{
    FreelistType: bolt.FreelistMapType,
    Timeout: 1 * time.Second,
  })
  if err != nil {
    return nil, err
  }

  return &indexStore{db: db}, nil
}

func (t *indexStore) Close() error {
  return t.db.Close()
}

func (t *indexStore) Size() (size uint64, err error) {
  err = t.db.View(func (tx *bolt.Tx) error {
    size = uint64(tx.Size())
    return nil
  })

  return
}

/*
func (t *indexStore) EntryCount() (entryCount uint64, err error) {
  err = t.db.View(func (tx *bolt.Tx) error {
    b := tx.Bucket(idxBucketName)
    if b == nil {
      return 0, errNotInitialized
    }

    entryCount = b.Sequence()
    return nil
  })

  return
}
*/

func (t *indexStore) Retrieve(key []byte) (entryId uint64, err error) {
  err = t.db.View(func (tx *bolt.Tx) error {
    b := tx.Bucket(idxBucketName)
    if b == nil {
      return errNotInitialized
    }

    val := b.Get(key)
    if len(val) == 0 {
      return errUnknownEntry
    }

    entryId, _ = decodeEntryId(val)

    return nil
  })

  return
}

func (t *indexStore) Has(key []byte) (has bool, err error) {
  err = t.db.View(func (tx *bolt.Tx) error {
    b := tx.Bucket(idxBucketName)

    if b == nil {
      return nil
    }

    has = b.Get(key) != nil
    return nil
  })

  return
}

func (t *indexStore) Put(key []byte, entryId uint64) error {
  return t.db.Update(func(tx *bolt.Tx) error {
    b, err := tx.CreateBucketIfNotExists(idxBucketName)
    if err != nil {
      return err
    }

    return b.Put(key, encodeEntryId(entryId))
  })
}

func (t *indexStore) DeleteAll(keys [][]byte) error {
  return t.db.Update(func(tx *bolt.Tx) error {
    b, err := tx.CreateBucketIfNotExists(idxBucketName)
    if err != nil {
      return err
    }

    for _, key := range keys {
      if err := b.Delete(key); err != nil {
        return err
      }
    }

    return nil
  })
}
