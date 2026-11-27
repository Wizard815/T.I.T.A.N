# One image, two commands: /titan (the board) and /titan-hands (the fixer).
FROM golang:1.24.9-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/titan ./cmd/titan \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/titan-hands ./cmd/titan-hands

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata && mkdir -p /data /config
COPY --from=build /out/ /
COPY titan.example.yaml /config/titan.example.yaml
ENV TITAN_CONFIG=/config/titan.yaml TITAN_DATA=/data/titan.db
EXPOSE 8130
HEALTHCHECK --interval=30s --timeout=5s CMD wget -qO- http://127.0.0.1:8130/health || exit 1
ENTRYPOINT ["/titan"]
