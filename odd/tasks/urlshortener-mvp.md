# urlshortener MVP v1 — Go + Gin, VSA + DDD + Hexagonal

URL shortener learning project (first rung of the learning ladder). Single monolith: one Go binary + PostgreSQL. Architecture: vertical slices, tactical DDD, hexagonal ports & adapters with an absolute dependency contract (see S5).

## Specs

- **S1**: Stack: Go + **Gin** + PostgreSQL. User: "me gusta mas Gin" (chi rejected).
- **S2**: Scope v1 ("MVP mínimo"): "Acortar, redirigir, validación de URLs, tests. Sin caché ni analytics."
  - `POST /shorten` with JSON `{"url": "..."}` → `201` with `{"code": "...", "short_url": "..."}`
  - `GET /:code` → `301` redirect to target (`Location` header); unknown code → `404`
  - Invalid URL (not absolute http/https, unparseable) → `422` with error message; rejected input must not persist anything
- **S3**: Identity: Base62 code generated from the sequence; collision with the unique index on `code` retries generation.
- **S4**: Monolith first: one Go binary, one process, one Postgres. User confirmed: "primero un monolito?" → "si".
- **S5**: Architecture contract (user: "una implementa que cumpla sus principios mas importantes y que permitan al software siempre escalar sin complicaciones o modificarse sin tanta complejidad"):
  1. Dependency rule absolute: business core imports nothing external (no Gin, no pgx, no SQL, no system clock).
  2. Every external interaction crosses a core-owned port (repository, code generation, time).
  3. Adapters are dumb translators; all business decisions live in domain or use cases.
  4. Boundaries by business capability (slices): new feature = new slice, existing slices untouched.
  5. Testability proves the design: core tests run without Docker/network/framework; sociable tests vs real Postgres (testcontainers) verify adapters only.
- **S6**: Data model: table `urls(id bigserial PK, code text UNIQUE NOT NULL, long_url text NOT NULL, created_at timestamptz NOT NULL default now())`.
- **S7**: Rich domain (rule #8): `ShortURL` entity with behavior (constructor applies validation + code assignment); no anemic structs, no business logic in handlers or repositories.
- **S8**: Errors: API problems as JSON (RFC 9457 problem details shape), no panics reaching users; unknown routes → 404 JSON.

## Tasks

- **T1** [S1,S4,S8] Scaffold: go module, cmd/api wiring, Gin router, health endpoint, config via env, docker-compose for Postgres. commit: `chore: scaffold go api with gin and compose` (0f56c9f)
- **T2** [S3,S5,S7] Domain: `ShortURL` entity (behavior: validate + build), Base62 code generator, URL validation rules. Pure unit tests (no Docker). commit: `feat(domain): shorturl entity with base62 codes and url validation` (bb13c01)
- **T3** [S5] Ports: `Repository` interface owned by the consuming slice/use case; errors as domain values. commit: with T4/T5 (interface lands with first consumer)
- **T4** [S1,S2,S5,S6,S7] Shorten slice: use case + Gin handler + pgx adapter + migration + sociable tests (testcontainers). commit: `feat(shorten): slice with use case, gin handler and pgx adapter` (9bd6575)
- **T5** [S2,S5,S6] Redirect slice: use case + Gin handler (301) + 404 + sociable tests. commit: `feat(redirect): slice with 301 redirect and 404 handling` (be4f652)
- **T6** [S5] ADR-001 in `docs/decisions.md`: Gin choice, hexagonal + VSA + DDD, principles contract, monolith-first. commit: `docs: adr-001 stack and architecture principles`

## Log

- **L1** (verbatim): "usar el mcp de excalidraw para crear diagrama de ejemplo como quedaria el diseño del urlshortener" — evolved into full design + implementation authorization.
- **L2**: User chose: purpose "Aprender system design", stack "Go", scope "MVP mínimo" ("Acortar, redirigir, validación de URLs, tests. Sin caché ni analytics").
- **L3**: "no quiero usar chi como framework me gusta mas Gin" → Gin.
- **L4**: "en lo de implementar VSA y yo que vamos a implemntar tambien conceptos de DDD tambien vamos a usar arquitectura hexagonal respetando los principios de las dependecias y la separacion del negocio con lo externo".
- **L5**: "la implementacion sea de clean architecture, hexagonal o cualquier otra arquitectura no quiero nada pragmatico que cumpla al pie de la letra como se implementan las cosas, no quiero eso quiero una implementa que cumpla sus principios mas importantes y que permitan al software siempre escalar sin complicaciones o modificarse sin tanta complejidad".
- **L6**: "antes de iniciar lo vas a crear que primero un monolito?" → confirmed monolith-first.
- **L7**: Implementation completed (T1-T6). Verification: full suite green (unit + handler + testcontainers e2e per slice); live smoke vs compose Postgres: healthz 200, POST /shorten 201 `{"code":"1","short_url":"http://localhost:18099/1"}`, GET /1 → 301 Location exact (query+fragment preserved), javascript: URL → 422, unknown code → 404. All commits on feature/urlshortener-mvp. Push/PR left as user decision.
