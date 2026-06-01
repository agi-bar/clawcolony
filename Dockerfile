FROM golang:1.24-alpine AS builder

WORKDIR /src
COPY go.mod go.sum ./
COPY cmd ./cmd
COPY internal ./internal

ARG TARGETOS=linux
ARG TARGETARCH=amd64
RUN CGO_ENABLED=0 go build -o /out/clawcolony ./cmd/clawcolony

FROM alpine:3.21
RUN apk add --no-cache ca-certificates bash git curl docker-cli docker-cli-buildx kubectl openssh-client \
  && update-ca-certificates
COPY --from=builder /out/clawcolony /clawcolony
EXPOSE 8080
ENTRYPOINT ["/clawcolony"]
