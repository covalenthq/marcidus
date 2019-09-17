package marcidus

import (
  "encoding/json"
  "errors"
  "fmt"
  "os"
  "path/filepath"

  "github.com/prometheus/tsdb/fileutil"
)

type configT map[string]interface{}

type Store struct {
  config       configT

  storeLock    fileutil.Releaser
  rowStore     *rowStore
  indexStore   *indexStore
}

var(
  errDuplicate = errors.New("entry already exists in store")
)

func readAndUpdateConfigManifest(manifestPath string, newConfig configT) (configT, error) {
  config := make(configT)

  if _, err := os.Stat(manifestPath); err == nil {
    manifestRdFile, _ := os.Open(manifestPath)
    defer manifestRdFile.Close()
    manifestDecoder := json.NewDecoder(manifestRdFile)
    err := manifestDecoder.Decode(config)
    if err != nil {
      return nil, err
    }
  }

  if newConfig != nil {
    for k, v := range newConfig {
      config[k] = v
    }

    manifestWrFile, _ := os.OpenFile(manifestPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
    defer manifestWrFile.Close()
    manifestEncoder := json.NewEncoder(manifestWrFile)
    err := manifestEncoder.Encode(config)
    if err != nil {
      return nil, err
    }
  }

  return config, nil
}

func Open(dataDir string, seqName string, newConfig configT) (*Store, error) {
  storeDir := filepath.Join(dataDir, seqName)

  if err := os.MkdirAll(storeDir, os.ModePerm); err != nil {
    return nil, err
  }

  storeLock, _, err := fileutil.Flock(filepath.Join(storeDir, "LOCK"))
  if err != nil {
    return nil, err
  }

  config, err := readAndUpdateConfigManifest(filepath.Join(storeDir, "manifest.json"), newConfig)
  if err != nil {
    return nil, err
  }

  if _, ok := config["stride"]; !ok {
    return nil, errors.New("a nonzero config[\"stride\"] is required for new marcidus.Store")
  }

  rowStore, err := newRowStore(filepath.Join(storeDir, "rows"), config["stride"].(int64))
  if err != nil {
    return nil, err
  }

  indexStore, err := newIndexStore(filepath.Join(storeDir, "index"))
  if err != nil {
    return nil, err
  }

  seq := &Store{
    rowStore:   rowStore,
    indexStore: indexStore,
    storeLock:  storeLock,
  }

  fmt.Printf("Opened marcidus.Store: %s\n", storeDir)
  return seq, nil
}

func (seq *Store) Close() error {
  var errs []error

  if err := seq.rowStore.Close(); err != nil {
    errs = append(errs, err)
  }

  if err := seq.indexStore.Close(); err != nil {
    errs = append(errs, err)
  }

  if err := seq.storeLock.Release(); err != nil {
    errs = append(errs, err)
  }

  if errs != nil {
    return fmt.Errorf("%v", errs)
  }

  return nil
}

func (seq *Store) HasID(entryId uint64) bool {
  return seq.rowStore.Has(entryId)
}

func (seq *Store) HasEntry(entryBlob []byte) (bool, error) {
  return seq.indexStore.Has(entryBlob)
}

func (seq *Store) GetEntry(entryId uint64) ([]byte, error) {
  return seq.rowStore.Retrieve(entryId)
}

func (seq *Store) GetID(entryBlob []byte) (uint64, error) {
  return seq.indexStore.Retrieve(entryBlob)
}

func (seq *Store) Count() (uint64, error) {
  return seq.rowStore.EntryCount(), nil
}

func (seq *Store) Size() (uint64, error) {
  rowStoreSize, err := seq.rowStore.Size()
  if err != nil {
    return 0, err
  }

  idxStoreSize, err := seq.indexStore.Size()
  if err != nil {
    return 0, err
  }

  return rowStoreSize + idxStoreSize, nil
}

func (seq *Store) TryInsert(entry []byte) (bool, error) {
  has, err := seq.indexStore.Has(entry)
  if err != nil {
    return false, err
  } else if has {
    return false, nil
  }

  assignedEntryId, err := seq.rowStore.Append(entry)
  if err != nil {
    return false, err
  }

  err = seq.indexStore.Put(entry, assignedEntryId)
  if err != nil {
    return false, err
  }

  return true, nil
}

func (seq *Store) Insert(entry []byte) error {
  inserted, err := seq.TryInsert(entry)
  if err == nil && !inserted {
    err = errDuplicate
  }
  return err
}

func (seq *Store) Truncate(newEntryCount uint64) error {
  removedEntries, err := seq.rowStore.truncate(newEntryCount)
  if err != nil {
    return err
  }

  return seq.indexStore.DeleteAll(removedEntries)
}

// sync flushes all data tables to disk.
func (seq *Store) Sync() error {
  var errs []error

  if err := seq.rowStore.Sync(); err != nil {
    errs = append(errs, err)
  }

  if errs != nil {
    return fmt.Errorf("%v", errs)
  }

  return nil
}

