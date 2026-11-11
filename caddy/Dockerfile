# UniFi HQ fork: bind the web assets and proxy configuration to one release image.
FROM caddy:2.11-alpine
COPY caddy/Caddyfile /etc/caddy/Caddyfile
COPY web /srv
RUN mkdir -p /etc/caddy/auth
LABEL org.opencontainers.image.title="UniFi HQ" \
      org.opencontainers.image.description="UniFi HQ traffic globe, inventory and topology" \
      org.opencontainers.image.licenses="GPL-3.0" \
      org.opencontainers.image.source="https://github.com/MephistoJB/unifi-HQ" \
      org.opencontainers.image.version="0.2.0"
