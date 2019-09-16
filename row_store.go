package casseq

import (
  "errors"
  "fmt"
  "os"
  "strings"
  "sync"
  "sync/atomic"
)

var (
  // errClosed is returned if an operation attempts to read from or write to the
  // freezer table after it has already been closed.
  errClosed = errors.New("closed")

  // errOutOfBounds is returned if the item requested is not contained within the
  // freezer table.
  errOutOfBounds = errors.New("out of bounds")

  // errNotSupported is returned if the database doesn't support the required operation.
  errNotSupported = errors.New("this operation is not supported")
)

type rowStore struct {
  entryStride    int64
  file           *os.File
  fileLock       sync.RWMutex

  entryCount     uint64
}

func newRowStore(filePath string, entryStride int64) (*rowStore, error) {
  file, err := os.OpenFile(filePath, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0644)
  if err != nil {
    return nil, err
  }

  tab := &rowStore{
    entryStride: entryStride,
    file:        file,
  }

  if err := tab.repair(); err != nil {
    tab.Close()
    return nil, err
  }

  return tab, nil
}

func (t *rowStore) repair() error {
  stat, err := t.file.Stat()
  if err != nil {
    return err
  }

  if overflow := stat.Size() % t.entryStride; overflow != 0 {
    t.file.Truncate(stat.Size() - overflow)

    if err := t.file.Sync(); err != nil {
      return err
    }
  }

  // Retrieve the file sizes and prepare for truncation
  if stat, err = t.file.Stat(); err != nil {
    return err
  }

  t.entryCount = uint64(stat.Size() / t.entryStride)

  fmt.Printf("rowStore opened: items=%d\n", t.entryCount)
  return nil
}

func (t *rowStore) truncate(newEntryCount uint64) ([][]byte, error) {
  t.fileLock.Lock()
  defer t.fileLock.Unlock()

  oldEntryCount := atomic.LoadUint64(&t.entryCount)
  if oldEntryCount <= newEntryCount {
    return nil, nil
  }

  fmt.Printf("Truncating rowStore: oldCount=%d newCount=%d\n", oldEntryCount, newEntryCount)

  removedBlobs := make([][]byte, (oldEntryCount - newEntryCount))

  for i := 0; i < len(removedBlobs); i++ {
    removedBlobs[i] = make([]byte, t.entryStride)
    if _, err := t.file.ReadAt(removedBlobs[i], t.entryStride); err != nil {
      return nil, err
    }
  }

  if err := t.file.Truncate(int64(newEntryCount) * t.entryStride); err != nil {
    return nil, err
  }

  atomic.StoreUint64(&t.entryCount, newEntryCount)

  return removedBlobs, nil
}

func (t *rowStore) Close() error {
  t.fileLock.Lock()
  defer t.fileLock.Unlock()

  if err := t.file.Close(); err != nil {
    return err
  }
  t.file = nil

  return nil
}

// Note, this method will *not* flush any data to disk so be sure to explicitly
// fsync before irreversibly deleting data from the database.
func (t *rowStore) Append(blob []byte) (uint64, error) {
  if int64(len(blob)) != t.entryStride {
    return 0, fmt.Errorf("appending unexpected item: want size=%d, have size=%d", t.entryStride, len(blob))
  }

  // Read lock prevents competition with truncate
  t.fileLock.RLock()

  // Ensure the table is still accessible
  if t.file == nil {
    t.fileLock.RUnlock()
    return 0, errClosed
  }

  defer t.fileLock.RUnlock()

  if _, err := t.file.Write(blob); err != nil {
    return 0, err
  }

  return atomic.AddUint64(&t.entryCount, 1) - 1, nil
}

func (t *rowStore) Retrieve(entryId uint64) ([]byte, error) {
  if t.file == nil {
    return nil, errClosed
  }

  if atomic.LoadUint64(&t.entryCount) <= entryId {
    return nil, errOutOfBounds
  }

  t.fileLock.RLock()
  defer t.fileLock.RUnlock()

  blob := make([]byte, t.entryStride)

  if _, err := t.file.ReadAt(blob, int64(entryId) * t.entryStride); err != nil {
    return nil, err
  }

  return blob, nil
}

func (t *rowStore) Has(entryId uint64) bool {
  return atomic.LoadUint64(&t.entryCount) > entryId
}

func (t *rowStore) EntryCount() uint64 {
  return atomic.LoadUint64(&t.entryCount)
}

func (t *rowStore) Size() (uint64, error) {
  t.fileLock.RLock()
  defer t.fileLock.RUnlock()

  stat, err := t.file.Stat()
  if err != nil {
    return 0, err
  }

  return uint64(stat.Size()), nil
}

func (t *rowStore) Sync() error {
  return t.file.Sync()
}

func (t *rowStore) printIndex() {
  entryBlob := make([]byte, t.entryStride)

  fmt.Printf("|  entryId | hex(bytes)\n")
  fmt.Printf("|----------|-%s\n", strings.Repeat("-", int(t.entryStride)))

  for entryId := int64(0); ; entryId++ {
    if _, err := t.file.ReadAt(entryBlob, entryId * t.entryStride); err != nil {
      break
    }
    fmt.Printf("| %08d | %#x\n", entryId, entryBlob)
  }
}
