# Tessera — Core Components (Architecture Reference)

> **Purpose.** One code-grounded map of every package in Tessera: what it is,
> where it lives, what it does, **what it deliberately does not do**, how the
> packages depend on one another, and how the **Ledger** consumes it. Every
> path/line/number below was verified against source on this branch with Go
> tooling (`go list`, `go doc`) and direct reads — *Code is Truth*: where a
> code comment or sibling doc disagrees with the code, the code wins (and the
> disagreement is flagged).
>
> **Scope verified at:** `transparency-dev/tessera` @ `de10380` ("Add max entry
> size limit", #859). This repo (`clearcompass-ai/tessera`) is a **vanilla
> upstream mirror** — 0 ClearCompass-specific Go files; all commits authored by
> the Transparency.dev maintainers. The Ledger (`clearcompass-ai/ledger`) pins
> `github.com/transparency-dev/tessera v1.0.2` and consumes it as a library.
>
> **Read in layers (each says a thing once):**
> 1. **Dictionary** — plain English + analogy + how many of each (start here).
> 2. **Reference** — the deep, code-grounded definition of each package.
> 3. **Limits & Verification** — every size/limit knob, and every verification depth.
> 4. **Pipelines** — how a request actually flows through the packages.
> 5. **Topology** — the real import DAG + how the Ledger consumes Tessera.

## The organizing principle

> **A library, not a service. Storage is blind; truth is recomputed.** Tessera
> ships as a Go *library* you link into your own binary (`NewAppender` hands
> back in-process objects and a shutdown func — there is no Tessera daemon in
> the core). All the "thinking" — Merkle math, proof construction, checkpoint
> signature checking — lives in a small set of packages (`storage/internal`,
> `client`, the root lifecycle). The **storage backends are content-blind**:
> leaves are opaque bytes, tiles are 32-byte hashes, and a test physically
> forbids storage from importing entry semantics. Because the artifacts are
> **static, deterministically-addressed, root-checked tiles**
> (`c2sp.org/tlog-tiles`), reads scale horizontally on dumb object stores and
> any client can re-verify offline. A backend that cannot read a payload cannot
> censor it; a verifier that recomputes the root does not need to trust the
> server that served the bytes.

Packages fall into seven classes: the **Core** (root lifecycle), the
**Contracts** (`api`, `api/layout`, root interfaces, `ctonly`), the **Client**
(read + verify), the **Storage Drivers** (`storage/*`), the **Internal Helpers**
(`internal/*`), the **Verification Tool** (`fsck`), and the **Commands**
(`cmd/*`). Plus the **Artifacts** they exchange.

---

# Layer 1 — Dictionary (plain English)

> The simple on-ramp. If you read only one section, read this. Each term gets
> three framings: **definition · think of it as · connects to**. No file paths
> here — those live in the Reference below.

## The whole system in one breath

In the broader Attesta map, *Tessera is "the filing cabinet and paper."* This
document opens that cabinet. Inside, picture a **self-service print-and-bind
shop for one append-only ledger book**: the **Appender** is the **foreman** who
takes loose slips, bundles them 256 to a page, and assigns each its permanent
page-and-line; the **integration engine** is the **math desk** that recomputes
the book's running fingerprint every time pages are added; the **Storage
Driver** is the **warehouse** (a filing cabinet, an S3 vault, a GCS vault, or a
MySQL vault) where pages are filed under a fixed numbering scheme; the
**checkpoint** is the **signed slip** stating "the book is exactly this many
lines long and its fingerprint is X"; **Witnesses** are **outside notaries** who
co-stamp that slip; the **Client** is the **reader's verification kit** that
re-derives the fingerprint from photocopied pages; **fsck** is an **auditor**
who re-photocopies the entire book and checks every page against the slip;
**antispam** is the **"have we filed this exact slip before?" index**.

## The terms

- **Appender / the Core** — the lifecycle that turns raw entries into a sequenced, integrated, signed log. *Think of it as:* the bindery foreman. *Connects to:* you call `Add`; it drives a storage Driver; it signs and (optionally) gets the checkpoint witnessed.
- **Driver / Storage backend** — the pluggable place the bytes actually live. *Think of it as:* the warehouse (filesystem / S3 / GCS / MySQL). *Connects to:* implements the lifecycle interface the Core calls; uses the shared integration engine; stores tiles & entry bundles.
- **Integration engine (`storage/internal`)** — the one shared Merkle-tree builder every backend uses. *Think of it as:* the math desk. *Connects to:* fed leaf hashes by a backend; returns the new root + the set of changed tiles; **stores nothing itself**.
- **Tile** — a fixed 256-wide slice of the Merkle tree (or 256 entries). *Think of it as:* one numbered, photocopyable page. *Connects to:* written by the engine, served statically, fetched + re-hashed by clients.
- **EntryBundle** — up to 256 length-prefixed leaf entries. *Think of it as:* the page of actual slips behind a tile. *Connects to:* the only thing migration copies; the source of antispam identities and leaf hashes.
- **Checkpoint** — a signed note: `origin`, `size`, `root hash`. *Think of it as:* the signed "table-of-contents fingerprint." *Connects to:* signed by the log key, optionally co-signed by witnesses, verified by every reader.
- **Witness** — an outside co-signer of checkpoints (a K-of-N policy). *Think of it as:* a notary who stamps the slip after checking it's consistent with the last one it saw. *Connects to:* contacted by the Core's checkpoint publisher; verified via consistency proofs. *(In Tessera the log drives the witnesses; the Ledger does NOT use this — see §Topology.)*
- **Client** — the read+verify toolkit. *Think of it as:* the reader's kit. *Connects to:* fetches checkpoint/tiles/bundles, verifies the checkpoint signature, and builds/checks inclusion & consistency proofs.
- **fsck** — the full-log re-derivation auditor. *Think of it as:* re-photocopy the whole book and check every page + the final fingerprint. *Connects to:* reads only public resources; needs no operator API.
- **Antispam** — best-effort duplicate suppression. *Think of it as:* the "seen this slip already?" index. *Connects to:* an in-memory LRU plus a persistent follower that tails the integrated log.
- **Migration** — import an existing tlog-tiles/CT log into a Tessera instance. *Think of it as:* re-binding an existing book into your warehouse and proving the fingerprint matches.

## How they interconnect (the counts)

| Relationship | Count | Plain English |
|---|---|---|
| Appender → Storage Driver | **1 → 1** | One appender drives exactly one backend instance |
| Storage Driver → integration engine | **many → 1** | All four backends share the one `storage/internal` engine |
| Log → Storage backend *types* | **1 of 4** | POSIX, AWS(S3+MySQL), GCP(GCS+Spanner), or MySQL-only |
| Backend → antispam impl | **1 → 0 or 1** | Optional; each backend family has a matching antispam store |
| Tree → Tiles | **1 → many** | Tiles are 256 wide (`TileHeight=8`), one per 256 nodes per level |
| Tile → EntryBundle | **1 ↔ 1** (level 0) | A leaf tile's 256 hashes correspond to one 256-entry bundle |
| Checkpoint → Witness cosignatures | **1 → 0..N** (need **K**) | A `WitnessGroup` policy; default group needs 0 |
| Log → Clients / fsck | **1 → many** | Anyone can read+verify anonymously from static tiles |
| Appender → checkpoint "clocks" | **2** | One synchronous sequence/integrate path; one periodic publish+witness path |

> **Worked example.** A binary calls `tessera.NewAppender` with a **POSIX**
> driver and a checkpoint signer. Each `Add(entry)` returns a future; the entry
> is batched (≤256 / ≤250 ms), durably sequenced, integrated into the tree by
> the shared engine, and the future resolves to its index. Independently, every
> 10 s a background loop signs a new checkpoint and — if a `WitnessGroup` is
> configured — sends it to witnesses for cosignatures before publishing. A
> reader fetches `checkpoint`, verifies its signature, fetches `tile/...`
> pages, and recomputes an inclusion proof — never trusting the operator.

```
   your binary
      │ NewAppender(driver, opts)
      ▼
   Appender.Add ──▶ [inMem dedup]→[size limit]→[stats]→[terminator]   (decorator chain)
      │ returns IndexFuture                         │
      ▼                                             ▼
   batching Queue ──flush──▶ backend sequence ──▶ storage/internal.Integrate ──▶ tiles
   (≤256 / ≤250ms)          (durable index)        (recompute root)            (static, 256-wide)
                                                         │
   periodic clock (every CheckpointInterval):            ▼
      sign note (log key) ──▶ Witness K-of-N ──▶ publish `checkpoint`
                                                         │ anyone reads (anon, offline)
                                                         ▼
                              Client / fsck recompute root from tiles, verify signature & proofs
```

---

# Layer 2 — Package Reference (code-grounded)

## Index (all 40 packages)

| Package | Class | Duty (one line) |
|---|---|---|
| `tessera` (root) | Core | Appender/Migration lifecycle, options, witness policy, checkpoint signing, entry model |
| `api` | Contract | The `tlog-tiles` wire artifacts: `HashTile`, `EntryBundle` |
| `api/layout` | Contract | Tile geometry (`TileHeight=8`) + deterministic path layout |
| `ctonly` | Contract | The CT-only entry type (RFC6962 back-compat) |
| `client` | Client | Read + verify: checkpoint fetch, `ProofBuilder`, `LogStateTracker` |
| `storage` | Storage | (directory only — no Go package; holds an architectural guard test) |
| `storage/internal` | Storage | The shared Merkle integration engine + batching `Queue` |
| `storage/posix` | Storage | Filesystem backend (single-process, `flock`) |
| `storage/aws` | Storage | S3 (tiles) + MySQL/Aurora (coordination) backend |
| `storage/gcp` | Storage | GCS (tiles) + Spanner (coordination) backend |
| `storage/mysql` | Storage | MySQL-only backend (tiles + bundles + state in SQL) |
| `storage/posix/antispam` | Storage | BadgerDB dedup index |
| `storage/aws/antispam` | Storage | MySQL dedup index |
| `storage/gcp/antispam` | Storage | Spanner dedup index |
| `internal/fetcher` | Helper | Partial→full tile fallback |
| `internal/parse` | Helper | **Unsafe** (signature-skipping) checkpoint field extraction |
| `internal/witness` | Helper | The runtime witness *client* (collects cosignatures) |
| `internal/migrate` | Helper | The `MigrationWriter` interface (engine lives in root) |
| `internal/otel` | Helper | Safe `uint64→int64` clamp for metrics |
| `internal/future` | Helper | A minimal resolve-once future |
| `fsck` | Verification | Independent full re-derivation of every tile + root |
| `cmd/fsck` (+ `tui`, `internal/tui`) | Command | fsck CLI + Bubbletea TUI |
| `cmd/conformance/{aws,gcp,mysql,posix}` | Command | Conformance/perf HTTP add servers, one per backend |
| `cmd/examples/posix-oneshot` | Command | Add entries to a local POSIX log and exit |
| `cmd/experimental/migrate/{aws,gcp,mysql,posix}` | Command | Import a source log into a Tessera instance |
| `cmd/experimental/mirror/{internal,posix}` | Command | Byte-copy a log's static resources (no verification) |
| `testonly` | Scaffolding | `NewTestLog` (temp POSIX log for tests) |
| `integration` (+ `fault/posix`) | Scaffolding | E2E + syscall fault-injection tests |
| `internal/hammer` (+ `loadtest`) | Scaffolding | Load-test driver |

> Format per entry: **Duty · Where (file:line) · Mechanism · Boundaries (what it
> does NOT do) · Limits · Verification.** Counts live in Layer 1; flow in Layer 4.

## The Core — `tessera` (root package)

### Duty
Own the **append lifecycle** and **migration lifecycle**: turn an `Entry` into a
durably-sequenced, integrated, signed (and optionally witnessed) log line, and
expose the read surface. It holds the option system, the entry model, the
witness *policy*, and checkpoint *signing*.

### Where
`append_lifecycle.go`, `lifecycle.go`, `entry.go`, `log.go`, `witness.go`,
`await.go`, `migrate.go`, `migrate_lifecycle.go`, `ct_only.go`, `antispam.go`,
`otel.go`.

### Mechanism
- **`NewAppender(ctx, Driver, *AppendOptions)`** (`append_lifecycle.go:254`) returns `(*Appender, shutdown func, LogReader, error)`. The `Driver` is the marker type `any` (`log.go:45`); the backend must satisfy an inline interface `Appender(ctx, *AppendOptions) (*Appender, LogReader, error)` (`append_lifecycle.go:255-256`).
- **The Add decorator chain** is assembled outermost-last (`append_lifecycle.go:272-303`): `addDecorators` (antispam: in-memory dedup + persistent decorator) → **entry-size limit** (`:275`, `entrySizeLimitDecorator` `:308` rejects `len(data) > maxEntrySize`) → **stats** (`:277`) → **terminator** (`:288`, refuses Adds after `Shutdown`, tracks largest issued index) → **memoize** (`:301`, `sync.OnceValues` so a future resolves once).
- **Checkpoint signing** — `WithCheckpointSigner` (`append_lifecycle.go:742`) builds `newCP` using `note.Sign` over a `formats/log.Checkpoint{Origin,Size,Hash}.Marshal()` (`:759-765`). An empty tree is forced to the RFC6962 empty root (`:755-758`). Additional signers must share the primary's name (which becomes the `Origin` line) (`:744-747`).
- **Checkpoint publishing + witnessing** — `CheckpointPublisher` (`append_lifecycle.go:642`) signs, then calls `witness.NewWitnessGateway(...).Witness(ctx, cp)` (`:669-677`) under a `WitnessOptions.Timeout`. `FailOpen` (`:679-685`) publishes anyway if the policy isn't met (an explicit, temporary adoption escape hatch).
- **Entry model** (`entry.go`): `NewEntry(data)` computes `Identity = SHA256(data)` (`:68`) and `LeafHash = rfc6962.HashLeaf(data)` (`:70`), and marshals into a bundle as `uint16(len) ‖ data` per tlog-tiles (`:73-78`).
- **`PublicationAwaiter`** (`await.go:53`) — `Await(ctx, future)` blocks until a published checkpoint commits to the entry's index (a poll loop reading the checkpoint, `:97`). This is how a personality returns "your entry is now provably in the log."
- **Migration** — `NewMigrationTarget` (`migrate_lifecycle.go:33`) + `Migrate` (`:109`): copies entry bundles (the `copier` in `migrate.go`, exponential backoff, 10 tries `:122`), re-integrates locally, and **fails closed unless the locally-computed root equals the source root** (`:181-183`).
- **Witness policy** (`witness.go`) — `WitnessGroup{Components, N}` (`:251`) is a tree of `Witness` or nested groups with a threshold; `Satisfied(cp)` counts components whose cosignature verifies (`:266`). `NewWitnessGroupFromPolicy` (`:52`) parses the Sigsum policy format with C2SP signed-note `0x04` cosignature vkeys; `quorum none` → threshold 0.

### Boundaries (what the Core does NOT do)
- **No storage, no servers.** The Core stores nothing and listens on nothing; it returns in-process objects. Durable bytes are the Driver's job.
- **No payload understanding.** It treats `entry.Data()` as opaque bytes; the only structure it imposes is the bundle length-prefix and the SHA256/RFC6962 hashes.
- **No fork *prevention*.** It signs whatever size/root it integrated; detecting equivocation is the reader/`fsck`/witness job (see §Verification).
- **CT mode is back-compat only.** `ct_only.go:37` documents that `NewCertificateTransparencyAppender` "MUST ONLY be used for CT logs … not … the basis for any other/new transparency application."

### Limits
`DefaultEntrySizeLimit = 1<<16 - 1` = **65,535 B** (`append_lifecycle.go:60`);
`DefaultBatchMaxSize = 256`, `DefaultBatchMaxAge = 250 ms` (`:41,43`);
`DefaultCheckpointInterval = 10 s`, `DefaultCheckpointRepublishInterval = 10 min`
(`:45,47`); `DefaultPushbackMaxOutstanding = 4096` (`:49`);
`DefaultGarbageCollectionInterval = 1 min` (`:51`);
`DefaultAntispamInMemorySize = 256<<10` = **262,144 entries** (`:55`);
`DefaultWitnessTimeout = 5 s` (`:57`); CT override `ctEntrySizeLimit = 256<<10`
= **262,144 B** (`ct_only.go:30`, set by `WithCTLayout` `:67`).

### Verification
Signs checkpoints (`note.Sign`); collects/threshold-checks witness cosignatures
(`WitnessGroup.Satisfied`); on migration recomputes and compares the root.

## The Contracts — `api`, `api/layout`, `ctonly`, and the root interfaces

### Duty
Define the **on-the-wire artifacts**, the **tile geometry/paths**, and the
**Go interfaces** that decouple the Core from the backends.

### Where & Mechanism
- **`api` (`api/state.go`)** — the two `tlog-tiles` artifacts:
  - `HashTile{Nodes [][]byte}` marshals as concatenated 32-byte hashes; unmarshal rejects non-multiples of 32 (`api/state.go:33-64`).
  - `EntryBundle{Entries [][]byte}` marshals as `uint16(len) ‖ data` repeated; unmarshal walks the length prefixes with dangling-byte guards (`api/state.go:66-93`).
- **`api/layout`** — the geometry & path spec:
  - `TileHeight = 8` (fixed by spec), `TileWidth = 1<<8 = 256`, `EntryBundleWidth = 256` (`api/layout/tile.go:17-25`).
  - `PartialTileSize`, `NodeCoordsToTileAddress` (`tile.go:30,41`); `Range(from,N,treeSize)` iterator over bundles (`paths.go:49`).
  - Paths: `CheckpointPath = "checkpoint"` (`paths.go:32`); `TilePath = "tile/{level}/{N}"` (`:127`); `EntriesPath = "tile/entries/{N}"` (`:121`); partial suffix `.p/{width}` (`:113`); the `N` is grouped in 3-digit `x`-prefixed chunks (`fmtN` `:139`); levels validated `0..63` (`:169`).
- **`ctonly`** — the CT entry type (`ctonly/`), used only via `WithCTLayout`; its `LeafData`/`MerkleLeafHash`/`Identity` feed `convertCTEntry` (`ct_only.go:51`).
- **The root interfaces** (`lifecycle.go`): `LogReader` (`ReadCheckpoint`, `ReadTile`, `ReadEntryBundle`, `NextIndex`, `IntegratedSize` — `:27`), `Follower` (`Name`/`Follow`/`EntriesProcessed` — `:73`), `Antispam` (`Decorator`/`Follower` — `:91`). The `Driver` seam is `any` (`log.go:45`) probed for `Appender`/`MigrationWriter` methods at construction.

### Boundaries
- `api/layout` is **pure** (imports nothing internal) — it is the lowest layer of the DAG.
- `LogReader.IntegratedSize` carries an explicit warning that it **must not** be used as a substitute for reading the checkpoint — it reflects integrated-but-maybe-not-yet-published state (`lifecycle.go:63-66`).

### Limits
`TileHeight=8`, `TileWidth=256`, `EntryBundleWidth=256` are **spec invariants**,
not tunables (`api/layout/tile.go:17-25`).

## The Client — `client`

### Duty
The **read + verify** toolkit for any tlog-tiles log (Tessera or not).

### Where
`client/client.go` (+ `fetcher.go`, `otel.go`).

### Mechanism
- **Checkpoint fetch + verify** — `FetchCheckpoint` (`client.go:104`) calls `formats/log.ParseCheckpoint(cpRaw, origin, v)`, which `note.Open`s the checkpoint and requires the **signature to match the log verifier and the origin to match** — an unsigned / wrong-key / wrong-origin checkpoint is rejected here.
- **Proofs** — `ProofBuilder` (`client.go:183`): `InclusionProof` (`:201`, `merkle/proof.Inclusion` + node fetch + `Rehash`) and `ConsistencyProof` (`:215`, `merkle/proof.Consistency`, bounded by the builder's tree size `:220`). Proof nodes are pulled through a `nodeCache` that fetches tiles and **synthesizes ephemeral (non-stored) interior nodes** from compact ranges (`:361-430`).
- **Consistency tracking** — `LogStateTracker.Update` (`client.go:305`) proves each newer checkpoint consistent with the last via `proof.VerifyConsistency` (`:328`); on failure it returns `ErrInconsistency{SmallerRaw, LargerRaw, Proof}` (`:329`) — i.e. it preserves the raw evidence of an inconsistent (forked) log.
- **Consensus hook** — `ConsensusCheckpointFunc` / `UnilateralConsensus` (`client.go:93,96`) let a caller plug a witnessed-consensus view in place of blindly trusting the source.
- **Fetcher contracts** — `TileFetcherFunc` / `EntryBundleFetcherFunc` MUST fall back from partial to full tiles and surface `os.ErrNotExist` (`client.go:69-87`).

### Boundaries
- **Does not witness-verify.** It checks the *log's* signature and Merkle proofs; checking *witness* cosignatures is left to the caller's `ConsensusCheckpointFunc`.
- **Not thread-safe per `ProofBuilder`** (`client.go:189-190`); `nodeCache` is per-request (`:359-360`).

### Limits & Verification
**No response-size bound** (see §Verification gap). Verification primitives:
checkpoint signature/origin, inclusion proof, consistency proof, and the
`ErrInconsistency` evidence path.

## The Storage Drivers — `storage/*`

### `storage` (directory)
**Not a Go package.** The only file is a black-box test that walks every
non-test `.go` file and **fails the build if a storage backend calls
`layout.EntriesPath`** instead of going through `AppendOptions.EntriesPath`
(`storage/storage_test.go:24-26,57-59`) — an architectural guard keeping path
policy in the Core, not the backends.

### `storage/internal` — the shared integration engine
- **Duty.** The backend-agnostic Merkle integrator + the in-memory batching `Queue`. (Package name is `storage`.)
- **Mechanism.** `Integrate(ctx, getTiles, fromSize, leafHashes) → (newSize, rootHash, tiles, err)` (`integrate.go:40`) builds the tree with `compact.RangeFactory{Hash: rfc6962.HashChildren}` (`:68`), **re-derives the old root from stored tiles before appending** ("verify the stored state", `:114-116`), appends the new leaves, recomputes the root, and **returns the set of dirty tiles for the backend to persist — it persists nothing itself.** The empty tree is special-cased to the RFC6962 empty root (`:122-124`). `NewQueue(ctx, maxAge, maxSize, FlushFunc)` (`queue.go:56`) flushes when full or a `time.AfterFunc(maxAge)` fires; it panics if a flush returns success but assigns no index — i.e. the backend forgot `MarshalBundleData` (`queue.go:178-179`).
- **Boundaries.** Owns Merkle math + batching + tile read/write caching; owns **no** durability, I/O, locking, or signing — those are injected per backend. The `treeBuilder` cache has **no eviction**; callers must bound its lifetime (`integrate.go:50-54`).
- **Verification.** Recomputes the prior root on every integration; surfaces tile-read errors via `tileWriteCache.Err()`.

### `storage/posix` — filesystem
- **Duty/Mechanism.** `New(ctx, Config{Path, HTTPClient})` (`files.go:106`). Pure filesystem (`os` calls); **no cloud SDK**. Sequencing + integration are **synchronous and inline** in the flush func (`files.go:259-383`). Concurrency uses a **double lock**: an in-process `sync.Mutex` *and* an advisory POSIX `flock` (`F_SETLKW`) on `.state/treeState.lock` (`files.go:176-201`). Writes are atomic temp-file + `link`/`rename` + directory `fsync` (`file_ops.go`). `NextIndex == IntegratedSize` because integration is inline (`files.go:249-251`).
- **Boundaries.** Single host only (flock doesn't scale across machines); no payload checks; signing delegated to `opts.CheckpointPublisher`.
- **Limits.** `minCheckpointInterval = 100 ms` (`files.go:65`, enforced `:133`); `compatibilityVersion = 1`; GC `maxBundlesPerRun = 100` ("Entirely arbitrary", `:706`); migration `maxBundles = 300`; `createTemp` retry cap `10000`; perms `0o755`/`0o644`.
- **Verification.** Root via the shared engine; `.state/version` pinned to 1; the fault-injection test (`integration/fault/posix`) proves the on-disk log is never left inconsistent under syscall failures, checked by `fsck`.

### `storage/aws` — S3 + MySQL/Aurora
- **Duty/Mechanism.** `New(ctx, Config)` (`aws.go:164`). **S3** (`*s3.Client`) stores tiles/bundles/checkpoint; **MySQL** stores the coordination tables (`Seq`, `SeqCoord`, `IntCoord`, `PubCoord`, `GCCoord`). Sequencing is **decoupled** from integration: `assignEntries` durably enqueues a gob batch (`:1020`), a 1 s background job consumes & integrates (`:267-292`). S3 idempotency via `IfNoneMatch:"*"` + byte-compare on `PreconditionFailed` (`:1434-1472`).
- **Boundaries.** No payload checks; S3 is blob-I/O only; durable ordering, pushback, publish-coordination, and GC all live behind the MySQL `sequencer` interface (`:102-125`).
- **Limits.** `minCheckpointInterval = 1 s` (`:74`); `DefaultPushbackMaxOutstanding = 4096` (`:76`); `DefaultIntegrationSizeLimit = 5*4096 = 20,480` (`:77`); integrate-job timeout 10 s; GC `maxBundlesPerRun = 100`; migration `maxBundles = 300`; cache-control `max-age=604800,immutable` for log objects, `no-cache` for the checkpoint (`:72-73`).
- **Verification.** Shared-engine root; consume path enforces sequence contiguity → "integrity fail" (`:1146-1148`); schema compatibility version 1.

### `storage/gcp` — GCS + Spanner
- **Duty/Mechanism.** `New(ctx, Config)` (`gcp.go:146`). **GCS** (`*gcs.Client`) for blobs; **Spanner** (6 tables) for coordination. Same decoupled model as AWS, but `integrateEntries` runs bundle-writing and tile-integration **in parallel** via `errgroup` (`:565-596`); Spanner uses `LOCK_HINT_EXCLUSIVE`. GCS idempotency via `Conditions{DoesNotExist:true}` + byte-compare on `PreconditionFailed` (handles both HTTP 412 and gRPC `FailedPrecondition`).
- **Limits.** `minCheckpointInterval = 1200 ms` — deliberately higher because GCS rate-limits object updates to ~1/s (`gcp.go:71-75`); `DefaultIntegrationSizeLimit = 20,480` (`:82`); **no `DefaultPushbackMaxOutstanding` const** — it reads `opts.PushbackMaxOutstanding()` directly (`:232`); GCS writer `ChunkSize = len+1024` to cap buffer memory (`:1225`).
- **Verification.** Shared-engine root; contiguity "integrity fail" (`:930-932`); `Tessera.compatibilityVersion` checked.

### `storage/mysql` — MySQL-only
- **Duty/Mechanism.** `New(ctx, *sql.DB) → *Storage` (`mysql.go:69`) — takes a caller-provided DB. **Everything** (tree state, tiles `Subtree`, bundles `TiledLeaves`, checkpoint) lives in SQL. Sequencing + integration are **synchronous in one transaction** with `SELECT … FOR UPDATE` row locking (`:421-547`). `NextIndex == IntegratedSize`.
- **Boundaries.** No object store; **no pushback** and **no GC** (no async path); `TODO(#21)` notes sequencing/integration aren't yet split for performance (`:420`).
- **Limits.** `minCheckpointInterval = 1 s`; bundle column is `LONGBLOB` to fit large CT leaves (`:927-933`); `cpUpdated` channel buffered to 1.

### `storage/{posix,aws,gcp}/antispam` — best-effort dedup
All three implement the `tessera.Antispam` contract (`Decorator` + `Follower`),
are **explicitly decoupled from tree storage**, **best-effort / eventually
consistent** (a follower tails the integrated log), and **first-writer-wins**.
- **POSIX → BadgerDB** (embedded KV): `DefaultMaxBatchSize = 1500`, `DefaultPushbackThreshold = 2048` (`badger.go:38-39`); value-log GC `0.7` every 1 min; matches POSIX's no-external-deps model.
- **AWS → MySQL** (`AntispamIDSeq` + `INSERT IGNORE`): `DefaultMaxBatchSize = 64` (smaller, tuned for row-insert throughput), `DefaultPushbackThreshold = 2048`, `SchemaCompatibilityVersion = 1` (`aws.go:39-49`); adds conn-pool/pushback knobs.
- **GCP → Spanner** (`IDSeq` + `BatchWrite` ignoring `AlreadyExists`): `DefaultMaxBatchSize = 1500`, `DefaultPushbackThreshold = 2048` (`gcp.go:46-47`); writes dedup rows **outside** the follower transaction to avoid retry storms on duplicate hashes (`:396-401`); has a `spannertest` emulator workaround.
- **Common verification.** Pushback (`ErrPushbackAntispam`) when `logSize - followFrom > PushbackThreshold`; `errOutOfSync` restart when the streamed index ≠ expected; progress persisted (`@nextIdx` / `FollowCoord.nextIdx`).

> **Cross-backend invariant.** Every backend's local `integrate(...)` is a thin
> wrapper over `storage.Integrate` (`posix:369`, `aws:818`, `gcp:614`,
> `mysql:604`), and every one builds `storage.NewQueue(ctx, opts.BatchMaxAge(),
> opts.BatchMaxSize(), flush)`. The Merkle math, tile geometry, futures, and the
> RFC6962 empty-root convention are identical; only **where bytes live** and
> **how writers coordinate** differ.

| Concern | POSIX | MySQL | AWS | GCP |
|---|---|---|---|---|
| Blob/tile store | filesystem | MySQL tables | S3 | GCS |
| Coordination | mutex + `flock` | single Tx `FOR UPDATE` | MySQL (5 coord tables) | Spanner (6 tables) |
| Seq ↔ integrate | inline (sync) | inline (sync) | **decoupled** (1 s job) | **decoupled** (parallel) |
| Pushback | none | none | const 4096 | `opts` (no const) |
| `minCheckpointInterval` | 100 ms | 1 s | 1 s | 1.2 s |
| Idempotent write | atomic link/rename | SQL upsert | S3 `IfNoneMatch` | GCS `DoesNotExist` |
| GC of partials | yes | no | yes | yes |
| Antispam store | BadgerDB | (n/a) | MySQL | Spanner |

## The Internal Helpers — `internal/*`

- **`internal/fetcher`** — `PartialOrFullResource(ctx, p, f)` (`fallback.go:27`): on `os.ErrNotExist` with `p>0`, retry the full tile (`p==0`), because partial tiles get promoted as the tree grows. **No size limit, no caching** — one function over `[]byte`.
- **`internal/parse`** — `CheckpointUnsafe(raw) → (origin, size, hash, err)` (`parse.go:36`): splits the note into 4 lines and parses size/hash. It **performs no signature verification at all** (documented `:29-35`: "Anyone copying similar logic into client code will get hurt"). Safe only inside the binary that already signed the checkpoint.
- **`internal/witness`** — the runtime witness **client**. `NewWitnessGateway` (`witness.go:102`) builds one `*witness` per endpoint from `group.Endpoints()`; `Witness(ctx, cp)` (`:130`) fans out, sends the tlog-witness request body (`old <size>\n` + base64 consistency proof + checkpoint, `:256-266`), and for each `200 OK` response calls `note.Open(cp ‖ response, VerifierList(verifier))` (`:301`) to **verify the cosignature** before accepting it, re-checking `group.Satisfied` after each new signature (`:177-196`). A `sharedConsistencyProofFetcher` memoizes the (usually shared) consistency proof (`:207-230`). `409` with `text/x.tlog.size` re-calibrates the witness's believed size and recurses (`:307-332`). State is **in-memory only** (not persisted across restarts).
- **`internal/migrate`** — the `MigrationWriter` interface only (`migrate.go:20`): `SetEntryBundle` (idempotent, any order), `AwaitIntegration(size) → root`, `IntegratedSize`. The engine is in the root package.
- **`internal/otel`** — `Clamp64(uint64) int64` (`cast.go:23`), used everywhere metrics take int64.
- **`internal/future`** — `FutureErr[T]` over a `WaitGroup` with a `sync.Once` setter (`future.go`); resolve-once, no cancellation.

## The Verification Tool — `fsck`

### Duty
Independently **re-derive the entire log from its entry bundles** and prove the
published tiles + signed checkpoint are correct — using only public resources.

### Where
`fsck/fsck.go`, `fsck/status.go`; driven by `cmd/fsck` (+ TUI).

### Mechanism — the six checks (each grounded)
1. **Checkpoint signature + origin** — `client.FetchCheckpoint(...)` → `log.ParseCheckpoint` → `note.Open` requiring matching key + origin (`fsck.go:87`). Rejects unsigned/wrong-key/wrong-origin up front.
2. **Per-bundle sequencing** — each bundle's implied start sequence must equal the running tree end (`fsck.go:249-252`).
3. **Leaf hashing** — bundles → leaf hashes via the injected `bundleHasher` (the CLI uses RFC6962 `HashLeaf`, `cmd/fsck/main.go:110-121`), appended into a `compact.Range`.
4. **Tile re-derivation, byte-for-byte** — derived tiles are compared `bytes.Equal` against the fetched real tiles, erroring with the `layout.TilePath` on mismatch (`fsck.go:343-347`).
5. **Partial tiles flushed** — trailing partial tiles at every level are emitted and checked too (`fsck.go:144,307-321`).
6. **Root equality** — the recomputed root must equal `cp.Hash`, else "calculated root … but checkpoint claims …" (`fsck.go:154-162`).

### Boundaries (what fsck does NOT check)
- **Witness cosignatures** — it verifies the *log* signature only.
- **Cross-checkpoint consistency** — it verifies one checkpoint in isolation; it does not check monotonicity against a previously seen state (that's `client.LogStateTracker`).
- **Entry semantics** — only what `bundleHasher` imposes.

## The Commands — `cmd/*`

- **`cmd/conformance/{aws,gcp,mysql,posix}`** — reference HTTP add-servers, one per backend, used for conformance + perf (each `tessera.NewAppender` + its backend + antispam).
- **`cmd/examples/posix-oneshot`** — add a list of entries to a local POSIX log and exit once integrated.
- **`cmd/experimental/migrate/{aws,gcp,mysql,posix}`** — drive `NewMigrationTarget(...).Migrate(...)` to import a tlog-tiles/static-ct source log; the **root==sourceRoot** check is the proof of a faithful import.
- **`cmd/experimental/mirror/{internal,posix}`** — byte-copy all static resources, writing the checkpoint **last**; explicitly performs **no** self-consistency/correctness checking (`mirror.go:49-56`) — it is a transport, paired with `fsck` for verification.
- **`cmd/fsck` (+ `tui`, `internal/tui`)** — the fsck CLI: `file://`→`FileFetcher`, else `HTTPFetcher` (+ optional bearer + `--qps` rate limiter); optional Bubbletea TUI.
- **Scaffolding** — `testonly.NewTestLog` (temp POSIX log); `integration` (E2E) and `integration/fault/posix` (strace syscall fault injection → fsck); `internal/hammer` + `loadtest` (load driver; its readers verify via `client.LogStateTracker`).

## Artifacts

| Artifact | Where | What it is |
|---|---|---|
| **Entry** | `entry.go` | Opaque `Data` + derived `Identity` (SHA256) + `LeafHash` (RFC6962); marshals as `uint16(len)‖data`; ≤ 65,535 B (or 256 KiB in CT mode) |
| **EntryBundle** | `api/state.go:66` | Up to 256 length-prefixed entries — one leaf "page" |
| **HashTile** | `api/state.go:33` | Up to 256 concatenated 32-byte node hashes — one tree "page" |
| **Checkpoint** | `append_lifecycle.go:742` (sign), `client.go:104` (verify) | Signed note `{Origin,Size,RootHash}` per `c2sp.org/tlog-checkpoint`; optionally witness-cosigned |
| **Inclusion / Consistency proof** | `client.go:201,215` | Merkle proofs built from tiles (ephemeral nodes synthesized) |
| **Witness cosignature** | `witness.go`, `internal/witness` | A K-of-N policy's co-signatures over a checkpoint, verified via `note.Open` |
| **Tile path** | `api/layout/paths.go` | Deterministic, content-free address (`tile/{lvl}/{N}`, `tile/entries/{N}`, `checkpoint`) |

---

# Layer 3 — Limits, Sizing & Verification (the two cross-cutting concerns)

## 3.1 — Every limit / sizing knob (consolidated)

| Knob | Value | Where | Notes |
|---|---|---|---|
| **Entry size (default)** | **65,535 B** (`1<<16 - 1`) | `append_lifecycle.go:60` | tlog-tiles cap; enforced by `entrySizeLimitDecorator` |
| **Entry size (CT mode)** | **262,144 B** (`256<<10`) | `ct_only.go:30,67` | set by `WithCTLayout` |
| Tile height / width | 8 / **256** | `api/layout/tile.go:20-22` | spec invariant, not tunable |
| EntryBundle width | **256** | `api/layout/tile.go:25` | = tile width |
| Batch max size / age | 256 / 250 ms | `append_lifecycle.go:41,43` | `WithBatching` |
| Checkpoint interval / republish | 10 s / 10 min | `append_lifecycle.go:45,47` | `WithCheckpointInterval` |
| Pushback max outstanding | 4096 | `append_lifecycle.go:49` | `WithPushback` |
| GC interval | 1 min | `append_lifecycle.go:51` | 0 disables |
| In-mem antispam cache | 262,144 entries | `append_lifecycle.go:55` | 40 B/entry → tiny |
| Witness timeout | 5 s | `append_lifecycle.go:57` | per publish attempt |
| Integration size limit (AWS/GCP) | 20,480 (`5*4096`) | `aws.go:77`, `gcp.go:82` | per consume batch |
| minCheckpointInterval | POSIX 100 ms · MySQL/AWS 1 s · GCP 1.2 s | `files.go:65`, `mysql.go:60`, `aws.go:74`, `gcp.go:75` | GCP higher: GCS rate limit |
| Antispam batch size | POSIX/GCP 1500 · AWS 64 | `badger.go:38`, `gcp.go:46`, `aws.go:39` | tuned per store |
| Antispam pushback threshold | 2048 (all) | `badger.go:39`, `aws.go:40`, `gcp.go:47` | follower lag bound |
| GC bundles/run | 100 | `files.go:706`, `aws.go:322`, `gcp.go:368` | "arbitrary" |
| Migration bundles/fetch | 300 | `files.go:1021`, `aws.go:572`, `gcp.go:1395` | |
| Cache-Control (AWS/GCP logs) | `max-age=604800,immutable` | `aws.go:72`, `gcp.go:77` | checkpoint = `no-cache` |
| Copier retries | 10, exponential | `migrate.go:122` | |

> ⚠️ **Verification gap worth flagging (Code is Truth).** There is **no
> response-size bound anywhere in the fetch path** — neither `internal/fetcher`
> nor the `client` fetchers use `io.LimitReader`/`MaxBytesReader` (grep-confirmed
> across both packages). A hostile or buggy source could return an arbitrarily
> large tile/bundle. The 65,535/262,144-byte caps apply to **entries on the
> write path**, not to bytes read back. (The Ledger's tile reader likewise sets
> no byte cap — see §4.3.)

## 3.2 — The verification depth ladder

Tessera offers a **spectrum** of verification, from "trust the same binary" to
"re-derive everything." Pick by threat model:

| Depth | Mechanism | Checks | Skips | Where |
|---|---|---|---|---|
| **0 — none (intra-binary)** | `parse.CheckpointUnsafe` | 4-line structural parse | **all signatures** | `internal/parse/parse.go:36` |
| **1 — checkpoint authenticity** | `client.FetchCheckpoint` → `note.Open` | log signature + origin match | proofs, witnesses | `client.go:104` |
| **2 — inclusion** | `ProofBuilder.InclusionProof` | leaf ∈ tree of size N | consistency | `client.go:201` |
| **3 — consistency / fork-evidence** | `LogStateTracker.Update` → `proof.VerifyConsistency` | append-only between sizes; emits `ErrInconsistency{raw evidence}` | witnesses | `client.go:305,328` |
| **4 — witness consensus** | `WitnessGroup.Satisfied` / `internal/witness` | K-of-N cosignatures (`note.Open` each) | — | `witness.go:266`, `internal/witness/witness.go:301` |
| **5 — integration self-check** | `storage.Integrate` | re-derives old root from stored tiles before each append | — | `storage/internal/integrate.go:114-116` |
| **6 — full re-derivation** | `fsck` | every tile byte-equal + root == checkpoint + sig/origin | witness cosigs, cross-checkpoint consistency | `fsck/fsck.go` |
| **7 — migration faithfulness** | `MigrationTarget.Migrate` | locally-computed root == declared source root (fail-closed) | — | `migrate_lifecycle.go:181-183` |

Two structural facts about this ladder:
- **The witness is detective, not preventative.** The Core signs whatever it
  integrated; witnesses and `client`/`fsck` are what *catch* a bad log. The
  cosignature protocol itself binds a consistency proof (`internal/witness`
  sends `old <size>` + proof), so a witness only co-signs an append-only step.
- **`CheckpointUnsafe` is deliberately depth-0** and is used in two places: the
  witness gateway (on the log's *own* freshly-signed checkpoint — safe) and the
  `mirror` tool (on an *untrusted source* — consistent with mirror's "copy
  only, verify nothing" contract; the verification is meant to come later from
  `fsck`).

---

# Layer 4 — Topology & how the Ledger consumes Tessera

> Derived from `go list -f '{{.ImportPath}} {{.Imports}}'` (production imports
> only — test edges excluded), not from prose.

## 4.1 — The internal dependency DAG (verified)

A clean one-way graph. The **pure leaves** import nothing internal; the **root
lifecycle sits in the middle** (it consumes `client`); the **storage backends
sit on top** (they consume the root + the integration engine).

```
   PURE LEAVES (import nothing internal):
     api/layout   ctonly   internal/fetcher   internal/parse
     internal/otel   internal/future   internal/migrate
        ▲
   api ────────────────▶ api/layout
        ▲
   client ─────────────▶ api, api/layout, internal/fetcher, internal/otel
        ▲
   tessera (root) ─────▶ api, api/layout, client, ctonly,
        ▲                internal/{migrate, otel, parse, witness}
        │
   storage/internal ──▶ (root), api, api/layout, internal/future, internal/otel
        ▲
   storage/{posix,aws,gcp,mysql} ─▶ (root), api, api/layout,
        ▲                            internal/{fetcher,migrate,parse}, storage/internal
        │
   storage/{posix,aws,gcp}/antispam ─▶ (root) and/or client
        │
   fsck ─▶ api, api/layout, client       cmd/* ─▶ (root) + storage backends
```

Key edges to internalize:
- **`api/layout` is the floor** — imported by 13 packages, imports nothing.
- **The root is NOT the bottom.** It depends on `client` (for migration's bundle fetcher and the witness gateway's proof builder) and on `internal/witness`. `client` does **not** import the root, so there's no cycle.
- **`storage/internal` depends on the root** (for `tessera.Entry`, `AppendOptions`, error types), and the **four backends depend on `storage/internal`** — so the dependency direction is `backend → storage/internal → root → client → api → api/layout`.
- **`fsck` reuses `client`** — verification is built on the same read path clients use.

| Package | Internal in-degree (who imports it) |
|---|---|
| `api/layout` (13) | root, api, client, fsck, storage/internal + 4 backends, 3 cmd, hammer/loadtest |
| `api` (9) | root, client, fsck, storage/internal + 4 backends, cmd/fsck |
| `client` (14) | root, fsck, all 3 antispam, 6 cmd, internal/witness, hammer(+loadtest) |
| `storage/internal` | storage/{posix,aws,gcp,mysql} |
| `internal/parse` | root, internal/witness, 2 cmd, storage/{aws,gcp,posix} |
| `internal/otel` | root, client, storage/gcp, storage/internal, storage/posix/antispam |

## 4.2 — External infrastructure per package

The "blast radius" of each heavy dependency is confined to its backend:

| Package | External infra it pulls in |
|---|---|
| `storage/posix` | filesystem only (+ otel) |
| `storage/aws` | `aws-sdk-go-v2` (S3) + `go-sql-driver/mysql` |
| `storage/gcp` | `cloud.google.com/go/spanner` + `cloud.google.com/go/storage` |
| `storage/mysql` | `go-sql-driver/mysql` |
| `storage/posix/antispam` | `dgraph-io/badger/v4` |
| `storage/aws/antispam` | `go-sql-driver/mysql` |
| `storage/gcp/antispam` | `cloud.google.com/go/spanner` |
| root, client, fsck | `transparency-dev/merkle` (rfc6962/compact/proof) + `golang.org/x/mod/sumdb/note` + `transparency-dev/formats` |

Verification/crypto is concentrated in `transparency-dev/merkle` (tree math) and
`sumdb/note` + `transparency-dev/formats` (checkpoint signing/verification);
neither the backends nor the leaves depend on heavyweight crypto beyond hashing.

## 4.3 — How the Ledger consumes Tessera

The Ledger (`github.com/clearcompass-ai/ledger`, pinning
`transparency-dev/tessera v1.0.2`) imports **exactly five** Tessera packages —
`tessera` (root), `client`, `api/layout`, `storage/posix`,
`storage/posix/antispam` — and confines all Tessera types to one integration
package (`ledger/tessera/`) plus the boot/cmd composition sites.

**Embedding (in-process, not HTTP).** `ledger/tessera/embedded_appender.go`
wraps `tessera.NewAppender` in an `EmbeddedAppender`, building options:
`WithCheckpointSigner(signer).WithCheckpointInterval(1s).WithBatching(256, 1s)`
and `WithAntispam(...)` when an antispam store is provided. It derives a private
background context so `Close` can deterministically drain Tessera's goroutines.
*(Several docstrings in these files mention an HTTP read path to a separate
Tessera "personality" — per Code-is-Truth the actual wiring is in-process /
direct-POSIX-disk; the HTTP language is stale.)*

**The seams (Ledger interfaces, Tessera plugged behind them):**
- `MerkleAppender` ← `TesseraAdapter` ← `AppenderBackend` ← `EmbeddedAppender` — proofs delegated to `client.NewProofBuilder(...).{Inclusion,Consistency}Proof` (`proof_adapter.go`).
- `TileBackend` ← `POSIXTileBackend` (reads tiles directly from disk by c2sp path) and `TileReader` (LRU cache, default 4096) whose `Fetch` implements `client.TileFetcherFunc` with the mandated partial→full fallback.
- `integrity/verifier.go` uses a `client.TileFetcherFunc` to answer "what 32-byte hash did Tessera commit at sequence N?" for WAL↔Tessera divergence detection — it does **not** verify proofs/consistency/checkpoints.

**What the Ledger delegates to Tessera:** sequence-number assignment ("Once
`Tessera.AppendLeaf` returns, the seq is irrevocably allocated"), Merkle tree
construction, leaf hashing (`rfc6962.HashLeaf`), checkpoint signing/publishing
(the `checkpoint` file), inclusion/consistency proof math, and duplicate
suppression (the antispam decorator).

**What the Ledger keeps (Tessera never sees it):**
- **Hash-only leaves** — Tessera receives exactly **32 bytes** (`SHA-256(canonical_bytes)`); the full envelope lives in the Ledger's `bytestore` (S3/GCS/memory), never in Tessera tiles. Tessera sees **zero domain logic**.
- **The Postgres/SMT projection** — derived by the SDK and rebuildable from tiles + bytestore.
- **Its own K-of-N cosigning** — the Ledger runs a separate quorum layer and publishes its own `cosigned-checkpoint`; **`tessera.WithWitnesses` is never used**, and Tessera's origin-signed `checkpoint` is explicitly distinguished from the Ledger's network-authoritative cosigned head.
- **Public proof endpoints** — tlog-tiles has no server-side proof API, so the Ledger computes proofs locally (via `client.ProofBuilder`) and serves them over its own HTTP API.

**Options the Ledger never sets** (so Tessera defaults apply): `WithPushback`
(stays 4096), `WithWitnesses`, `WithCTLayout`, `WithGarbageCollectionInterval`,
`WithCheckpointRepublishInterval`, and any explicit entry-size limit (the
default 65,535 applies, but the Ledger only ever appends 32 bytes).

> **The throughline.** Tessera is the Ledger's **content-blind sequencer + tile
> store**: it owns ordering, Merkle math, and proof construction over opaque
> 32-byte leaves; the Ledger owns identity, payload storage, witnessing, and the
> authoritative cosigned checkpoint. The same `storage.Integrate` engine and
> `client.ProofBuilder` that a standalone Tessera log uses are the ones running
> inside the Ledger — which is exactly why a verifier can re-derive the Ledger's
> Merkle evidence from public POSIX tiles with no operator API.

---

## Appendix — How this document was verified

- **Package set:** `go list ./...` → 40 packages (enumerated in the Index).
- **DAG:** `go list -f '{{.ImportPath}}|{{join .Imports " "}}' ./...`, filtered to `github.com/transparency-dev/tessera/*` for §4.1, and to external modules for §4.2.
- **API surface:** `go doc -short <pkg>` for every package; reads of every root file, the `api`/`api/layout`/`client` sources, and representative storage/internal sources.
- **Limits:** grep of numeric `const`/`var` declarations across the tree; each row in §3.1 carries its `file:line`.
- **Ledger consumption:** `go list` of `transparency-dev/tessera` imports inside `clearcompass-ai/ledger`, plus reads of `ledger/tessera/*` and the boot/cmd/integrity sites.
- **Fork status:** `git log` author histogram + grep for `clearcompass` in `*.go` (0 hits) confirm `clearcompass-ai/tessera` is a vanilla upstream mirror at commit `de10380`.
