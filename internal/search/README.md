# internal/search — SearchRouter V1 + web provider adapters

Búsqueda estructural de la organización: routing declarativo por `SearchIntent`,
sin selección LLM de proveedor. El `Router` es la única autoridad para
selección, fallback, sufficiency, cache, deduplication y provenance.

## Web providers (V1)

Implementados como adapters HTTP deliberadamente simples (un request, parseo,
normalización a `[]SearchResult`):

| Provider | ProviderID  | Endpoint canónico (default)                       | Auth |
|----------|-------------|---------------------------------------------------|------|
| Brave    | `brave_web` | `https://api.search.brave.com/res/v1/web/search`  | header `X-Subscription-Token` |
| Tavily   | `tavily`    | `https://api.tavily.com/search`                   | `api_key` en body JSON (POST) |
| SerpAPI  | `serpapi`   | `https://serpapi.com/search`                      | query param `api_key` (GET) |
| Google   | `google_web`| `https://www.googleapis.com/customsearch/v1`     | query params `key` + `cx` |

Ruta web canónica **inalterada**: `BraveWeb -> Tavily -> SerpAPI -> GoogleWeb`.
Google permanece como último fallback; no es proveedor por defecto. La ruta
`news` sigue siendo `Tavily -> SerpAPI -> GoogleWeb` (Tavily usa `topic: "news"`
y SerpAPI `engine=google_news`; Brave no anuncia `IntentNews`).

Cada adapter inyecta un `HTTPDoer` (interfaz estrecha con `Do(*http.Request)`)
o un `*http.Client` envuelto — nunca usa `http.DefaultClient` — lo que permite
tests con `httptest.Server` sin red externa.

## Configuración / env

Prefijo canónico (mismo patrón que `ORG_MODEL_PROVIDER_*` de modelruntime):

```
ORG_SEARCH_PROVIDER_<NAME>_ENABLED          (bool, default false)
ORG_SEARCH_PROVIDER_<NAME>_ENDPOINT_URL     (url, default tabla de arriba)
ORG_SEARCH_PROVIDER_<NAME>_CREDENTIAL_FILE  (ruta absoluta a la key)
ORG_SEARCH_PROVIDER_<NAME>_REQUEST_TIMEOUT  (duration, default 15s)
ORG_SEARCH_PROVIDER_<NAME>_MAX_RETRIES      (int, default 1)
ORG_SEARCH_PROVIDER_GOOGLE_ENGINE_ID        (engine id `cx` de Custom Search)
```

con `<NAME>` ∈ `BRAVE | TAVILY | SERPAPI | GOOGLE`.

Las credenciales se resuelven en este orden (idéntico a `readSecret` del org):

1. `ORG_SEARCH_PROVIDER_<NAME>_CREDENTIAL_FILE`
2. `/etc/explorarte/secrets/<basename>` — para Brave/Tavily ya montado en
   producción (`/run/secrets/brave-api-key`, `/run/secrets/tavily-api-key`).
   Basenames: `brave-api-key`, `tavily-api-key`, `serpapi-api-key`,
   `google-search-api-key`.
3. `/run/secrets/<basename>`

Un provider sin credencial queda deshabilitado (no registrado, sin panic,
sin bloquear startup). Construcción explícita y testeable vía
`NewWebProviders(lookup, doer)` + `WebProviderSet.RegisterInto(registry)` —
sin service locator global.

Programa una key ausente como error claro, no panic. Google requiere además
`engine_id` (config error tipado si falta).

## Taxonomía de errores

`ProviderErrorKind` + `ProviderError{Provider, Kind, StatusCode, RetryAfter,
Cause}` clasifica fallos sin string-matching:

`bad_request`, `unauthorized`, `forbidden`, `rate_limited`, `timeout`,
`unavailable`, `internal`, `malformed_response`. Los mensajes de error jamás
incluyen API keys, URLs de request, ni bodies; las URLs secret-bearing se
guardan fuera de logs/errores.

## Política de retry V1

Máximo **1 retry**, sólo para transitorios claros: timeout de transporte,
`408`, `429`, `500`, `502`, `503`, `504`. Sin retry para `400/401/403/404`.
`429` con `Retry-After` > 2s (o desconocido) devuelve `rate_limited` sin
dormir; con `Retry-After` corto hace un único retry y nunca bloquea el worker
minutos.

## Tests

- `brave_test.go`, `tavily_test.go`, `serpapi_test.go`, `googleweb_test.go`:
  httptest por provider (200/empty/malformed/401/403/429+Retry-After/5xx/
  timeout/secrets no filtradas/authenticator).
- `webconfig_test.go`: config/env/credential resolution, disabled default,
  registro apto de absent providers.
- `router_web_test.go`: integración Router↔adapters (CASE A–G):

  A) Brave suficiente → `Brave=1, Tavily=0, SerpAPI=0, Google=0`
  B) Brave 500 → Tavily suficiente → `SerpAPI=0, Google=0`
  C) Brave/Tavily insuficientes → SerpAPI suficiente → `Google=0`
  D) todos insuficientes/error → Google suficiente → `Google=1`
  E) misma request repetida → cache hit, external calls = 1 total
  F) Brave 401 → sin retry inútil, fallback a Tavily; usage/observability
     registran `unauthorized` + `provider_failed`
  G) Brave 429 con `Retry-After: 600` → error tipado `rate_limited`, sin
     sleep, fallback a Tavily, usage marca `rate_limited`

`go test ./internal/search/...` (123 tests), `go test ./...`,
`go vet ./internal/search/...`, `go vet ./...`, `go test -race
./internal/search/...` están en verde.

## Scope NO cubierto (V1)

- Sin adapters HTTP para OpenAlex/arXiv/Crossref/PubMed/Unpaywall/Google
  Books/DOAB/OAPEN/Gutendex/OpenLibrary/Semantic Scholar.
- Sin persistencia de cache ni ledger; sin Redis; sin LLM routing; sin
  reranker neural.
- Los adapters NO deciden fallback/sufficiency/cache/dedupe/provenance;
  no se tocan `RolePolicy`, `SearchIntent`, ni la arquitectura central.