# Architecture Decision Records

Decision records for the URL shortener. One file, one record per decision that
merits it: stack, architecture, or contract changes with a lasting why. Small
routine choices stay in code comments and PR descriptions.

| ADR     | Title                                          | Status   | Date       |
| ------- | ---------------------------------------------- | -------- | ---------- |
| ADR-001 | Stack and architecture of the URL shortener v1 | Accepted | 2026-11-03 |

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
