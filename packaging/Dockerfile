# Local cross-platform binary build. Supply the pinned mcptype checkout with
# --build-context mcptype=../mcptype; no source or artifacts are published.
FROM rust:1.99.1-slim-bookworm AS build
COPY --from=golang:1.27.0-bookworm /usr/local/go /usr/local/go
RUN apt-get update && apt-get install -y --no-install-recommends musl-tools pkg-config python3 && rm -rf /var/lib/apt/lists/*
ENV PATH="/usr/local/go/bin:${PATH}"
COPY --from=mcptype . /mcptype
WORKDIR /mcptype
ARG TARGETARCH
RUN --mount=type=cache,target=/usr/local/cargo/registry --mount=type=cache,target=/mcptype/target,id=cli-mcptype-${TARGETARCH} \
    triple=$(rustc -vV | sed -n 's/^host: //p') && triple="${triple%-gnu}-musl" && \
    rustup target add "$triple" && \
    python3 ffi/package-sdk.py /opt/mcptype "$triple" && \
    cp "$(rustc --print target-libdir --target "$triple")/self-contained/libunwind.a" /opt/mcptype/lib/ && \
    sed -i 's/-lmcptype /-lmcptype -lunwind /' /opt/mcptype/lib/pkgconfig/mcptype.pc
WORKDIR /src
COPY . .
ENV CGO_ENABLED=1 CC=musl-gcc CGO_LDFLAGS=-static PKG_CONFIG_PATH=/opt/mcptype/lib/pkgconfig
ARG VERSION=0.0.1
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build,id=cli-go-${TARGETARCH} \
    go build -trimpath -buildvcs=false -tags netgo,osusergo \
      -ldflags "-s -w -X main.version=${VERSION} -linkmode external -extldflags -static" -o /out/holon ./cmd/holon
FROM scratch
COPY --from=build /out/ /
