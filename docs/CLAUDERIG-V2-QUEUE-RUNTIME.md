# Claude queue runtime (7a)

The internal `service.QueueRuntime` supplies the persisted local associations
needed by opt-in queued Claude commands. Commands and hooks still use the existing
synchronous path; this slice installs no worker and exposes no cleanup command.

## One lifecycle, fixed stores

`CreateQueueRuntime` accepts freshly resolved sync configuration, explicit Desktop
profiles and a private runtime root. The root must be outside every enabled
source and canonical staging tree. A runtime requires a configured remote. The
caller retains existing remote privacy/authentication checks and stable local
filesystem ownership, as for the artifact APIs.

A new runtime owns this layout:

```text
runtime/
  runtime.json       # checksummed lifecycle descriptor and producer identities
  queue/             # existing durable queue format
  captures/          # retained captures; shared seeds are captures/seeds
  commits/           # retained commits and publication/recovery scratch
```

The root is created exclusively. Its descriptor contains a cryptographically
random lifecycle ID, a hash of the canonical root location, the capture-policy
binding and a bounded map of producer identities. It contains no raw config,
remote URL, credentials or transcript bytes. Private identity values are limited
to the existing validated account UUID, organization UUID and email fields.

The queue binding adds the lifecycle ID and canonical root to the store
fingerprint. Two runtimes with identical capture configuration consequently have
different queue bindings. The adapter validates that binding before translating
to the existing capture-policy binding; immutable captures retain their current
format and keys within their separate stores. No queue schema changes are needed.

`OpenQueueRuntime` loads existing metadata and queue state. It never initializes
or resets either. A missing/corrupt descriptor or queue, mismatched capture policy,
foreign lifecycle, moved root, or linked managed child refuses the operation.
Capture and commit directories may be absent until first use. The descriptor is
bounded to 1 MiB and 1,024 identities; hashes, identity shape, version and decoded
fields are validated before use.

Creation publishes the queue before its descriptor. An interruption before the
descriptor is published can leave an incomplete directory; a retry refuses it
and requires explicit inspection. It never treats an existing empty directory as
a fresh runtime. Retrying a fully written descriptor reopens and durably reflushes
both queue state and descriptor before reporting success. Missing queue state is
never recreated. Existing runtime objects also reject a changed lifecycle ID.

These are cooperative local ownership guarantees, not protection against an
operator who forges checksummed metadata or manually transplants archive contents.
Never copy/reset individual children or share them with independent consumers.
The runtime does not adopt legacy arbitrary artifact stores. Reclamation is not
wired by this slice: its rollout must use this fixed association and must retain
the queue lifecycle, rather than accepting independently selected paths.

## Producer attribution and retry

`Enqueue` accepts one identity observation made by the producer, a request and the
original enqueue timestamp. It validates and durably saves identity **before**
calling the queue's durable enqueue. An identity write failure, including an
uncertain write, never accepts an event. Retries reflush existing identity records
before retrying enqueue, and the queue preserves its existing event deduplication.
Concurrent producers serialize descriptor updates and do not lose identities.

The producer must retain the original identity, event ID and timestamp across
retries. A later login is not a substitute. Explicit unknown identity is stored
as such; a missing identity record during execution is an error. A failed enqueue
can leave an unused identity record. Identities are retained and bounded; reaching
the identity limit refuses new identities while allowing known producers to
continue. This slice does not compact identity history.

## Restart and execution

`Adapter` accepts a callback for freshly resolved configuration, selected profiles,
transport and archive limits. The callback supplies neither archive paths nor
producer identity: those come from the runtime layout and durable descriptor.
Each batch rechecks the capture binding, lifecycle metadata and queue state before
using the saved producer identity. It never reads the worker's live login.
Archive limits remain runtime admission policy and can change without rewriting
capture identity, using the existing artifact-store semantics.

`CheckStartup` preserves the shared Git history check with the runtime's stores.
The caller still supplies it to `Queue.Run`, owns platform command supervision,
and handles stop/drain and foreground coordination. A saved committed phase can
resume publication after restart without the original transcript. A runtime's
adapter refuses batches claimed from another lifecycle.

The runtime root lease precedes queue transaction access during producer and
resolver operations. Do not invoke runtime methods inside `Queue.Maintain` or
another held queue transaction. Adapter resolution runs after the shared worker
has released its claim transaction. Runtime initialization uses nonwaiting queue
maintenance ownership when revalidating an existing lifecycle.

## Rollout still to connect

1. Explicit queue commands: initialization, acceptance, worker startup/status,
   retry and draining, using existing Git/`gh` privacy/authentication and the
   supervised executable entry point.
2. Opt-in hook routing, bounded producer input, synchronous coverage and rollback.
3. Reclamation exposure only through the retained runtime association, plus actual
   OS restart/hibernation validation before general release.

Synthetic tests cover failed/uncertain identity writes, retry deduplication,
concurrent producers, bounded metadata, unknown attribution, missing/reset/foreign
queues, source overlap, moved roots, linked stores, changed config and an offline
committed-phase restart that publishes the original producer's bytes/attribution.
The pinned v1 compatibility baseline remains unchanged.
