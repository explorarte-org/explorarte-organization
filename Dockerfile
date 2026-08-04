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

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/orgd /usr/local/bin/orgd
COPY --from=build /out/orgctl /usr/local/bin/orgctl
COPY --from=build /src/docs/canonical /opt/explorarte/docs/canonical
ENV ORG_CANONICAL_DIR=/opt/explorarte/docs/canonical
USER 65532:65532
EXPOSE 8080
HEALTHCHECK --interval=10s --timeout=3s --start-period=15s --retries=5 CMD ["/usr/local/bin/orgctl", "health", "--ready", "--url", "http://127.0.0.1:8080"]
ENTRYPOINT ["/usr/local/bin/orgd"]
