# Lagezentrum-API: Gerätenamen aus dem Dashboard speichern (config/hosts.csv)
FROM golang:1.27-alpine AS build
RUN apk add --no-cache ca-certificates
WORKDIR /src
COPY go.mod *.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /lz-api .

FROM scratch
LABEL org.opencontainers.image.source="https://github.com/MephistoJB/unifi-HQ" \
      org.opencontainers.image.licenses="GPL-3.0" \
      org.opencontainers.image.version="0.2.0"
COPY --from=build /lz-api /lz-api
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
ENTRYPOINT ["/lz-api"]
