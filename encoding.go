package casseq

func encodeEntryId(entryId uint64) []byte {
  var blob []byte
  var limit uint64
  limit = 32
  for bytecount := 1; bytecount <=8; bytecount++ {
    if entryId < limit {
      blob = make([]byte, bytecount)
      b := entryId
      for i := bytecount - 1; i > 0; i-- {
        blob[i] = byte(b&0xff)
        b >>= 8
      }
      blob[0] = byte(b) | (byte(bytecount)<<5) // 3 most significant bits of the first byte are bytecount
      break
    }
    limit <<= 8
  }
  return blob
}

func decodeEntryId(blob []byte) (uint64, []byte) {
  bytecount := int(blob[0]>>5)
  entryId := uint64(blob[0]&0x1f)
  for i := 1; i < bytecount; i++ {
    entryId = (entryId<<8) | uint64(blob[i])
  }
  return entryId, blob[bytecount:]
}
