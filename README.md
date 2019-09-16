# Casseq

Casseq is a library defining a simple, filesystem-backed "indexed array",
called `casseq.Store`.

The design-goal of this data structure, is to serve as an efficient means of
creating, storing, and querying **surrogate keys** in database systems,
for times when the *natural key* for referencing a type of item would be very
large, and prohibitively costly to build indices upon.

Examples of such costly natural keys:

* 16-byte event UUIDs
* 32-byte SHA256 hashes
* 20-byte blockchain addresses

In any case where your system is taking data and analyzing it "in private", and
does not need to expose its own internal representation for foreign keys,
a surrogate-key mapping can be used to "compress" the keyspace of the natural
keys, transforming the above into simple `uint64`s.

## Design

A `casseq.Store` is a bidirectional mapping from `[]byte` values to `uint64`
insertion-order ID numbers.

As with a content-addressible store, new values cannot be inserted with an
arbitrary key, but rather values are inserted "bare", and then a
key (the insertion-order `entryId`) is returned to represent them.

As well, just like with a content-addressible store, values cannot be removed
or changed once inserted. If this is a desired property, a separate index
would have to be kept to represent which `entryId`s are still "valid" in an
application-semantic sense.

However, the entire store can be *truncated* to remove all values inserted after
a given `entryId`, essentially "rewinding" the store to holding only what it
held when the specified entry was the newest one.

As `casseq.Store` is intended for the compression of fixed-width natural
keys, it expects and requires your `[]byte` values to have a fixed, known size
(though this size may vary per `Store`); this allows for several optimizations
in reading/indexing values.

## Implementation

A `casseq.Store` is represented on disk by a directory containing two files:

* `rows`: a fixed-width append-only flat file, containing all entry data in a
  contiguous stream. This file does not explicitly encode `entryId`s, but rather
  they are implicit as the positions of the entries in the file. Each entry
  can be found at the byte-position of `entryId * stride`.

* `index`: a [bbolt](https://github.com/etcd-io/bbolt) (essentially LMDB)
  database, holding a single B-tree table using entries as keys,
  and (a compact encoding of) `entryId`s as values.

These files are updated in synchrony when a new entry is inserted. `index` can
be recreated from `rows` if it is damaged/missing.
