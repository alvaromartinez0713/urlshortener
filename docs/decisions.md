# Architecture Decision Records

Decision records for the URL shortener. One file, one record per decision that
merits it: stack, architecture, or contract changes with a lasting why. Small
routine choices stay in code comments and PR descriptions.

| ADR     | Title                                          | Status   | Date       |
| ------- | ---------------------------------------------- | -------- | ---------- |
| ADR-001 | Stack and architecture of the URL shortener v1 | Accepted | 2026-11-03 |
| ADR-002 | Testing pillars and conventions                | Accepted | 2026-11-03 |

## ADR-001: Stack and architecture of the URL shortener v1

Status: Accepted — 2026-11-03

### Context

Learning project and first rung of a ladder: urlshortener → rate limiter →
chat → feed → AI-native. The goal is not the product but practicing VSA,
tactical DDD, and hexagonal architecture on a small system where the whole
design fits in one head (Specs S1–S8, Log L1–L6 of `odd/tasks/urlshortener-mvp.md`).

### Decisions

- **Go + Gin** (S1, L3). User preference ("me gusta más Gin"; chi rejected) plus
  ecosystem fit. The router is a delivery detail behind the hexagon — swapping
  it must not touch the core.
- **PostgreSQL** as the only datastore (S1, S6). One table (`urls`) is the
  source of truth; the unique index on `code` is the collision arbiter (S3).
- **Monolith first** (S4, L6). One Go binary, one process, one Postgres.
  Scaling path: the service is stateless, so scale out is horizontal
  replication of the binary; Redis (caching) and analytics are not separate
  units (it.2/it.3 stay inside the monolith) and only split out on a justified
  trigger.
- **Vertical Slice Architecture** (S5.4, L4). Code is navigated by business
  capability (`internal/shorten`, `internal/redirect`); a new feature is a new
  slice, existing slices stay untouched.
- **Tactical DDD, rich domain** (S7, L4). `NewShortURL` validates the target
  URL and assigns the code in the constructor; no anemic structs, no business
  logic in handlers or repositories.
- **Hexagonal, dependency rule absolute** (S5.1–S5.3, L4, L5). The core imports
  nothing external: no Gin, no pgx, no SQL, no system clock. Every external
  interaction crosses a core-owned port (`Repository`, `CodeGenerator`, `Finder`).
  Adapters are dumb translators; all business decisions live in the domain or
  use cases.
- **Principles over recipes** (L5). No architecture is followed to the letter;
  what is enforced are its load-bearing principles. Success test for every
  decision is the 5-point contract of S5: (1) absolute dependency rule,
  (2) every external interaction through a core-owned port, (3) dumb-adapter
  translators, (4) boundaries by business capability, (5) core tests run
  without Docker/network/framework; only sociable adapter tests need real
  Postgres (testcontainers). In practice: "can I change or replace this
  without pain?"
- **Errors as RFC 9457 problem details** (S8). API errors are JSON problem
  documents; no panics reach users; unknown routes return 404 JSON.
- **Base62 codes from the database sequence, collision retry bounded at 3**
  (S3). On a unique-index collision the use case consumes a fresh sequence
  value and retries; exhausting the bound is a 500.

### Consequences and trade-offs

- **301 is permanent** (S2). Codes are immutable in v1, so a cached 301 is
  correct. Mutable codes later would require 302 or cache-busting.
- **Sequence gaps are accepted.** Codes are opaque; gap-free allocation is not
  a goal and would couple the generator to transactional locking.
- **Sociable tests need Docker** (S5.5). Core unit tests stay pure, but adapter
  tests via testcontainers make Docker a CI dependency.
- **Deliberate YAGNI (S2, L2):** no cache, no analytics, no auth in v1. Each
  stays out until a measured trigger justifies it.

### References

- `odd/tasks/urlshortener-mvp.md` (Specs S1–S8, Log L1–L6)
- `docs/diagrams/c1-context.png`, `docs/diagrams/c2-container.png`,
  `docs/diagrams/c3-components.png`, `docs/diagrams/overview-hexagonal.png`

## ADR-002: Testing pillars and conventions

Status: Accepted — 2026-11-03

### Context

The hexagonal core of ADR-001 promised that core tests run without Docker,
network, or frameworks (S5.5), and the user made fast test feedback a standing
principle: "los tests se deben ejecutar lo más rápido posible" (Spec S12, Log
L10). Early use-case tests asserted interaction counts (`gen.calls`,
`finder.calls`) — white-box spies that pin HOW the core talks to its ports
instead of WHAT it produces, and that break on any faithful refactor. The
conventions were applied in T10 and are recorded here so they survive future
slices.

### Decisions

- **Testing trophy, applied to hexagonal.** Most tests are fast unit/slice
  tests over the core; a thin layer of sociable e2e tests exercises real
  adapters. No test pyramid of many slow end-to-end tests.
- **Entry points only.** Slice tests enter through a public entry point: the
  handler (HTTP: request in, status + body out) or the use case (public
  method). The domain is tested via its public API (`NewShortURL`,
  `Base62Encode`). No test reaches into unexported state.
- **Doubles are only implementations of core-owned ports.** Hand-written
  fakes (`fakeRepo`, `fakeSequences`, `fakeFinder`), deterministic through
  their configured INPUTS. No mock frameworks, ever.
- **Mocking own collaborators is forbidden.** The core's ports belong to the
  core; a fake implementation states behavior, a mock states expectations.
- **Behavior-only assertions.** Assert the observable outcome: final code,
  final error (via `errors.Is`), the value returned through the port, zero
  persistence on rejection. No interaction counting where the count is not
  the contract itself. Fakes model their limits through inputs — e.g. a
  sequence source that holds exactly `maxCodeAttempts` values and then
  errors, instead of a spy that counts calls (S12, T10).
- **Fast feedback (S12, L10).** Core packages (`shorturl`, use cases, httpx,
  api) run with no Docker and finish in milliseconds. Container-based
  packages share ONE Postgres testcontainer across their tests, with
  per-test data isolation, so wall-clock feedback stays minimal (lands in
  T11).
- **Sociable e2e with real Postgres** via testcontainers (S5.5): handler →
  use case → domain → real repository, one slow-but-real check per slice.

### Consequences and trade-offs

- **Refactors stay free.** Behavior-only tests do not pin call sequences;
  reordering internal calls (as long as observable behavior holds) keeps the
  suite green.
- **The fast path is still provable.** "The port is not reached" is asserted
  by configuring the fake to succeed if reached and asserting the use case's
  error wins — behavior, not a counter.
- **Fakes are small, maintained code.** Hand-written doubles cost a little
  upkeep per port; this is accepted in exchange for zero mock-framework
  dependencies and total determinism.
- **CI still needs Docker** for the sociable e2e tests (inherited from
  ADR-001); core packages do not.

### References

- `odd/tasks/urlshortener-mvp.md` (Spec S5.5, S12; Log L10; tasks T10, T11)
- ADR-001 (hexagonal ports and the S5.5 testing contract)
- `internal/shorten/usecase_test.go`, `internal/redirect/usecase_test.go`
  (fakes and behavior-only assertions in practice)
