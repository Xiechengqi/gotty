# Frontend assets are platform independent; build them on the build host.
FROM --platform=$BUILDPLATFORM node:24 AS js-build
WORKDIR /gotty
COPY js/package*.json /gotty/js/
RUN cd js && npm ci
COPY js /gotty/js
COPY resources /gotty/resources
COPY Makefile /gotty/
RUN make assets

FROM --platform=$BUILDPLATFORM golang:1.26 AS go-build
ARG TARGETARCH
ARG TARGETOS
ARG VERSION=dev
WORKDIR /gotty
RUN apt-get update && apt-get install -y --no-install-recommends curl ca-certificates && rm -rf /var/lib/apt/lists/*
COPY . /gotty
COPY --from=js-build /gotty/bindata/static /gotty/bindata/static
RUN test "$TARGETOS" = linux && ./scripts/prepare-http-tunnel-client.sh "$TARGETARCH"
RUN GOOS=$TARGETOS GOARCH=$TARGETARCH CGO_ENABLED=0 go build -trimpath -ldflags "-X main.Version=$VERSION" -o /gotty/gotty .

FROM alpine:3.24
RUN apk --no-cache add ca-certificates bash
WORKDIR /root
COPY --from=go-build /gotty/gotty /usr/bin/
CMD ["gotty", "-w", "bash"]
