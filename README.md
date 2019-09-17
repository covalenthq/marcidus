# Marcidus

![Psychrolutes marcidus, a.k.a. the smooth-headed BLOBfish.](https://github.com/covalenthq/marcidus/raw/docs/images/exemplar.png)

Marcidus is a hybrid Content- and Location-Addressable BLOB Store library,
written in Go. It is intended to be used as a local cache of a distributed
Content-Addressable Store, for storing local copies of e.g. `git` objects, or
blockchain transactions, or virus signatures.

Marcidus allows BLOBs to be tracked and retrieved by either/both of a
**content hash** (e.g. a SHA256 hash), or a **globally-unique identifier** /
**GUID** (e.g. a normalized URI, or a UUID).

As Marcidus is a local store, it also can (and does!) assign **locally-unique
identifiers** or **LUIDs** (i.e. `uint64` insertion-order serial IDs) to your
BLOBs, which are more compact than either GUIDs or content-hashes, at the
expense of creating an upper bound on the number of BLOBs a single Marcidus
store can track. (Though, if you need to fit more than 2^64 BLOBs in a *single
local cache*, there's probably a story there. Get in touch!)

The LUID is the "true" primary key of your BLOBs in Marcidus; lookups by GUID or
by content-hash must first resolve the input key representation to an LUID,
before looking up the BLOB data by its LUID. As such, tracking Marcidus BLOBs in
your external systems by their Marcidus LUID is the most efficient strategy in
terms of per-lookup overhead and retrieval latency, if you can get away with it.

Marcidus's content-storage architecture was heavily inspired by go-ethereum's [freezer](https://blog.ethereum.org/2019/07/10/geth-v1-9-0/#freezer) subsystem. Marcidus
can be used as a standalone replacement for this subsystem for anyone who wants
to use something like it in their own code.

## API Core Concepts

Marcidus has three core abstract data-types: the **Store**, the **Handle**,
and the **BLOB**. As well, a **StoreManager** may be used to abstract away some
of the details.

#### `marcidus.Store`

A `marcidus.Store` is a durable, indexed BLOB store for a single *kind* of content.
Content *kinds* are defined by the combination of three properties:

* the Store's **GUID format** — either a `FormatDefinition`, or `nil` to not
  track BLOB GUIDs
* the Store's **content-hash format** — either a `FormatDefinition`, or `nil` to
  not track BLOB content-hashes
* the Store's **content-hashing strategy** — either a `HashAlgorithm`, or `nil`
  to disallow BLOB data→content-hash derivation

BLOBs encoding data of nominally-different data types may be mixed into a
single `marcidus.Store`, if all such data has the same content *kind* (i.e. if
all such BLOBs have the same needs regarding GUIDs + content-hashing.) However,
there is no special advantage in doing so; Marcidus does not keep any
application-layer in-memory caches, rather relying on the disk cache to make
memory-mapped IO fast. Code that interacts with one Marcidus store of size 2N,
vs. two Marcidus stores each of size N, should have equivalent performance.

A `marcidus.Store` where both the GUID and content-hash formats are `nil` is
still a valid store, as every BLOB still has at least an LUID key. However,
without either content-hashes or GUIDs to build a unique index upon, insertion
idempotency (i.e. deduplication of BLOB data) cannot be (efficiently) checked,
and so is not checked at all. BLOBs must be inserted into the store
passing `&InsertOptions{Deduplicate: false}` to acknowledge this.

If the `marcidus.Store` is configured with a *content-hash format* but not a
*content-hashing strategy*, all BLOB insertions are required to pass an explicit
`contentHash`. If a *content-hashing strategy* is set, an explicit `contentHash`
may still be passed (and will be used/trusted, for efficiency's sake), but it
may also be left out, whereupon the `Store` will use the configured
`HashAlgorithm` to automatically derive a content-hash for the given BLOB data,
and store it alongside the data. Having a configured *content-hashing strategy*
also allows you to ask a `Store` whether a given `[]byte` of BLOB data exists in
the store; the `Store` will automatically derive the content-hash for the given
data, and then check for the presence of that content-hash.

Each `marcidus.Store` is backed by a filesystem directory. Further details can
be found in the [Data Architecture section](#data-architecture).

#### `marcidus.Handle`

A `marcidus.Handle` is a reference to an individual BLOB in an individual
`marcidus.Store`.

Rather than having a single concrete representation, a `Handle` can be thought
of as a incrementally-prepared query against a `marcidus.Store`. A `Handle` is
created using a GUID, content-hash, or LUID. The `Handle` can then be *resolved*
which will use the `Handle`'s associated `marcidus.Store` to find other
available key representations given the ones available in the `Handle`, and then
cache those representations into the `Handle` as well.

The usual goal of `Handle` resolution is to lower bulk lookup overhead,
by taking a collection of GUID- or content-hash-initialized `Handle`s and
caching their LUID representations into the `Handle`s as well. However,
`Handle` resolution is also how you would look up the GUID or the content-hash
of a BLOB, given another key representation.

At any point, the `Handle` can be used to retrieve the BLOB itself (as a
`marcidus.BLOB`) from its associated `Store`.

#### `marcidus.BLOB`

A `marcidus.BLOB` is just a struct containing both a `marcidus.Handle`,
together with the relevant BLOB's data as a `[]byte`.

A successful `marcidus.Handle` data retrieval results in a `marcidus.BLOB`.

A `marcidus.BLOB` is also the input type for insertion of a new BLOB into
a `marcidus.Store`. You must populate the data `[]byte`, together with any of
the `Handle`'s fields that are required by the `Store`'s content *kind* and
cannot be automatically derived. Population of the `Handle`'s `LUID` field will
be ignored.

#### `marcidus.StoreManager`

A collection of `marcidus.Store`s may be further abstracted by making
use of the `marcidus.StoreManager` (a global registry for
`marcidus.Store` objects.) The `marcidus.StoreManager` allows `marcidus.Handle`s
to be constructed, resolved, and retrieved on, by reference to a
`marcidus.Store`'s "registered ID" within the `StoreManager`, avoiding the need
to pass `marcidus.Store` objects around between your components.

## Data Architecture

A `marcidus.Store` combines several data representations within its backing
directory. Depending on the `Store`'s content *kind*, any of the following
files may or may not be present in the `Store`'s backing directory.

* `blobs_NNN.plog` — the set of [`PartitionedLogFile`](#marciduspartitionedlogfile)
  extent data-files holding BLOB data.

* `blobs.idx.af` — the [`PartitionedLogFile`](#marciduspartitionedlogfile) extent
  index-file listing BLOB positions within the extent data-files, in
  `struct{ExtentId uint16, Offset uint32}` format. This file is also an
  [`ArrayFile`](#marcidusarrayfile) with a *stride* of 6. BLOBs' LUIDs are
  *defined* as the index of their position-record within this file.

* `hashes.af` — an [`ArrayFile`](#marcidusarrayfile) recording BLOB content-hash
  keys (if applicable), using the same ordering as `blobs.idx.af`, allowing
  content-hashes to be retrieved by LUID.

* `guids.af` — an [`ArrayFile`](#marcidusarrayfile) recording BLOB GUID
  keys (if applicable), using the same ordering as `blobs.idx.af`, allowing
  GUIDs to be retrieved by LUID. This file exists if the GUID format is
  fixed-width.

* `guids.log` and `guids.idx.af` — a [`LogFile`](#marciduslogfile) extent
  data-file and index-file, recording BLOB
  GUID keys (if applicable), using the same ordering as `blobs.idx.af`, allowing
  GUIDs to be retrieved by LUID. These files exist if the GUID format is
  variable-width.

* `luids.db` — an LMDB B-tree file (specifically, a
  [bbolt](https://github.com/etcd-io/bbolt) database file), containing a
  *unique* index mapping either GUIDs or content-hashes to LUIDs. This index is
  used to deduplicate BLOBs upon insertion. If your `Store` uses both GUIDs and
  content-hashes, this file will use whichever key representation results in
  smaller keys.

Helpful notes:

* LUIDs are mostly implicit in the on-disk data; they are represented in
  `blobs.idx.af`, `hashes.af`, and `guids.af` only as the **positions** of the
  records in the files. This means that Marcidus's LUIDs are not *generated*
  and *assigned* (with the possibility of reassignment), but rather are an
  inherent *property* of the dataset: the initial insertion order of the BLOBs.
  Reassigning, swapping, or culling LUIDs would require rewriting the underlying
  `blobs_NNN.plog` and `blobs.idx.af` files to reorder the BLOB data.

* LUIDs *are* explicitly recorded in `luids.db`, as the values of LMDB keys.
  These values use a compact, lexicographically-sortable representation of the
  LUID: `struct{Len uint8, Mantissa []byte}`, where `Mantissa` is a
  Big-Endian-serialized integer with all leading zero-bytes stripped, and
  `Len` is `len(Mantissa)`.

* If you delete `luids.db`, it will be rebuilt when the `Store` is opened from
  either `guids.af` or `hashes.af`, as applicable. This is a blocking operation.

## Internal Disk-backed ADTs

These might be extracted into their own libraries later. For now, they live in
here.

#### `marcidus.ArrayFile`

This ADT simulates a fixed-record-width "record file" or "recordset" in a
[record-oriented filesystem][1].

[1]: https://en.wikipedia.org/wiki/Record-oriented_filesystem

This ADT backs `blobs.idx` and `hashes.dat`, and `guids.dat` as well if the
specified *GUID format* is fixed-width.

`ArrayFile`s are initialized with a **stride**: a fixed record size, measured
in bytes. This *stride* gets recorded into a short, fixed-size *header* in the
backing file. The `ArrayFile` ADT then allows for random read-write access to
records in the backing file given an *index*, which the `ArrayFile` converts
into a *file offset* by just multiplying the *index* by the *stride*, then
adding an offset to skip past the *header*.

Helpful notes:

* If a filesystem supporting sparse file-extents is used, an `ArrayFile` will
  be able to semi-efficiently represent a collection with sparse identifiers.

#### `marcidus.PartitionedLogFile`

This ADT exposes random-access reads, append-only writes, and record-granular
truncations, and is backed by a set of variable-record-width *extent data*
files, together with a single fixed-record-width *record index* file.

Or, to put it another way, this ADT reimplements the go-ethereum `freezer_table`
ADT.

This ADT backs `blobs.dat.NNN`, and `blobs.idx` in a transitive sense
(`PartitionedLogFile` delegates to `ArrayFile` to manage fixed-record-width
index files.)

Helpful notes:

* Each extent data file has a maximum design capacity of 4GiB, to allow for
  32-bit file offsets to be used to refer to record positions within the file.
  However, a `PartitionedLogFile` can be configured with a lower per-extent
  capacity. Finer-grained partitioning allows for more efficient use of e.g.
  `rsync(1)` in backing up these file sets, as all but the newest extent-file
  will stay unmodified and keep the same checksum.

* Individual records are never split between files, but instead are just placed
  wholly into the next extent in the series, if they do not fit entirely in the
  current extent. This means that not all extent files will reach their exact
  capacity.

* The *extent index* file uses the `ArrayFile` ADT, with a *stride* of 6, to
  encode `struct{ExtentId uint16, Offset uint32}` records.

* A record's *length* is not recorded explicitly in the extent index-file. It is
  instead derived upon retrieval as the difference between the Offset from the
  selected record and the Offset from the next sequential record in the same
  extent data-file; or, if there are no more records in the extent data-file,
  it is derived as the difference between the record's Offset and the extent
  data-file's file-size.

#### `marcidus.LogFile`

This ADT is a simplification of `marcidus.PartitionedLogFile`, removing the
extent-partitioning logic. A `LogFile` has just one extent data-file. This
greatly simplifies the code and avoids several corner-cases in crash recovery
that `PartitionedLogFile` is vulnerable to.

`LogFile` makes sense for small tuple-like records. It probably makes more sense
to use `PartitionedLogFile` for large document-like records, where the store
will quickly grow to exceed one extent after only a few million.

This ADT backs `guids.dat` if the specified *GUID format* is variable-width.

Helpful notes (see also the notes for `PartitionedLogFile`):

* The *extent index* file of a `LogFile` uses the `ArrayFile` ADT. The records
  in the extent index are plain big-endian integers, representing the offsets in
  the *extent data file*.

* The *stride* of a `LogFile`'s *extent index* can be configured upon
  initialization of the `LogFile`. It defaults to 8. If you know your *extent
  data file* will never exceed a certain file-size threshold, then you can
  set this number lower, and the *extent index* will be made more compact and
  (slightly) faster to read. This can also be thought of as specifying a
  capacity for the `LogFile`; appends to the `LogFile` will fail if they would
  cause the file to exceed the specified capacity.
