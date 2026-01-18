FROM golang:1.25.1 AS builder

WORKDIR /workspace
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/status-collector ./cmd/status-collector

FROM gcr.io/distroless/static:nonroot

WORKDIR /
COPY --from=builder /out/status-collector /status-collector

USER nonroot:nonroot

ENTRYPOINT ["/status-collector"]
