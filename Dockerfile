# syntax=docker/dockerfile:1.7

FROM --platform=$BUILDPLATFORM golang:1.25-bookworm AS build
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_TIME=unknown
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.buildTime=${BUILD_TIME}" -o /out/orgd ./cmd/orgd
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.buildTime=${BUILD_TIME}" -o /out/orgctl ./cmd/orgctl

# Non-default target for `orgctl rag ingest-pdf` and `orgctl corpus
# census` (owner decision: PDF parsing must not live in orgd/core -- see
# internal/pdfingest's package doc comment; internal/corpuscensus follows
# the same boundary discipline and reuses this same image rather than
# getting a third one). Distroless/static below cannot run poppler-utils
# or sqlite3 at all (dynamically linked against several C libraries);
# this stage is the only place in this Dockerfile poppler-utils/sqlite3
# are installed, and orgd never runs from it. sqlite3 here is a read-only
# reader of a paper harvester's state DB (internal/corpuscensus/
# corpuscensus_bronze.go invokes it with `-readonly`), not anything
# durable to this organization's own state. Deliberately placed BEFORE
# the orgd stage below (not merely named): `docker build .` with no
# --target builds the LAST stage in the file by default, and the orgd
# distroless image must always be that default -- this stage is only
# ever built explicitly via `docker build --target pdfingest`.
FROM debian:bookworm-slim AS pdfingest
RUN apt-get update && apt-get install -y --no-install-recommends poppler-utils ghostscript sqlite3 ca-certificates && rm -rf /var/lib/apt/lists/* && useradd --system --no-create-home --shell /usr/sbin/nologin pdfingest
COPY --from=build /out/orgctl /usr/local/bin/orgctl
COPY --from=build /src/docs/canonical /opt/explorarte/docs/canonical
ENV ORG_CANONICAL_DIR=/opt/explorarte/docs/canonical
USER pdfingest
ENTRYPOINT ["/usr/local/bin/orgctl"]

FROM gcr.io/distroless/static-debian12:nonroot AS orgd
COPY --from=build /out/orgd /usr/local/bin/orgd
COPY --from=build /out/orgctl /usr/local/bin/orgctl
COPY --from=build /src/docs/canonical /opt/explorarte/docs/canonical
ENV ORG_CANONICAL_DIR=/opt/explorarte/docs/canonical

# Immutable, sanitized Context Engine document source: an explicit allowlist
# of exactly the organization/department AGENT.md and role PERFIL.md tree
# that internal/contextengine/document.Loader resolves paths against
# (ORG_CONTEXT_SOURCE_ROOT). Each COPY names a single file or a known unit
# directory -- never the repo root or a wildcard -- so .env, .git, secrets,
# docs/, config/, deployments/, migrations/, scripts/, cmd/, internal/ can
# never end up here even if new top-level files/dirs are added later.
COPY --from=build /src/AGENT.md /opt/explorarte/context-source/AGENT.md
COPY --from=build /src/negocio /opt/explorarte/context-source/negocio
COPY --from=build /src/ingenieria_ia /opt/explorarte/context-source/ingenieria_ia
COPY --from=build /src/recursos_agenticos /opt/explorarte/context-source/recursos_agenticos
COPY --from=build /src/servicios /opt/explorarte/context-source/servicios
COPY --from=build /src/empresa /opt/explorarte/context-source/empresa
COPY --from=build /src/investigacion /opt/explorarte/context-source/investigacion
ENV ORG_CONTEXT_SOURCE_ROOT=/opt/explorarte/context-source

USER 65532:65532
EXPOSE 8080
HEALTHCHECK --interval=10s --timeout=3s --start-period=15s --retries=5 CMD ["/usr/local/bin/orgctl", "health", "--ready", "--url", "http://127.0.0.1:8080"]
ENTRYPOINT ["/usr/local/bin/orgd"]
