# Build stage
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
ARG TARGETOS TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build \
    -ldflags="-s -w -X main.version=${VERSION}" \
    -o /goku ./cmd/goku

# Run stage
FROM alpine:3.21
RUN apk --no-cache add ca-certificates
COPY --from=build /goku /usr/local/bin/goku
COPY config/config.yaml /etc/goku/config.yaml

ENV GOKU_CONFIG=/etc/goku/config.yaml
EXPOSE 9001
ENTRYPOINT ["goku"]
