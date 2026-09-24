FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/fngbot . && mkdir /out/data

# The final image holds only the binary and CA certificates.
FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /out/fngbot /fngbot
COPY --from=build --chown=65534:65534 /out/data /data
USER 65534:65534
WORKDIR /
# Reads /config.yaml and keeps the dedup state in /data/state.json (the default data/state.json).
ENTRYPOINT ["/fngbot"]
