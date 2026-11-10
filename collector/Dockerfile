# Collector: Vector mit eingebautem GoFlow2.
# GoFlow2 läuft als Kindprozess von Vector (exec-Source) und schreibt seine
# JSON-Zeilen direkt in die Pipeline: kein Docker-Socket, keine Log-Dateien,
# keine zusätzlichen Schreibzugriffe auf die SSD.
ARG GOFLOW2_VERSION=v2.2.7
ARG VECTOR_VERSION=0.58.0

FROM netsampler/goflow2:${GOFLOW2_VERSION} AS goflow2

FROM timberio/vector:${VECTOR_VERSION}-alpine
COPY --from=goflow2 /goflow2 /usr/local/bin/goflow2
COPY --chmod=644 vector.yaml /etc/vector/vector.yaml
RUN chmod 755 /etc/vector
# Vector 0.58 requires an explicit opt-in for the nonsecret port substitutions.
# Controller and login secrets are mounted exclusively into the separate API.
ENV VECTOR_DANGEROUSLY_ALLOW_ENV_VAR_INTERPOLATION=true
LABEL org.opencontainers.image.source="https://github.com/MephistoJB/unifi-HQ" \
      org.opencontainers.image.licenses="GPL-3.0" \
      org.opencontainers.image.version="0.1.0"
# Unprivilegierter Benutzer; die Ports 2055/5514/8090 liegen über 1024
USER 65534:65534
ENTRYPOINT ["/usr/local/bin/vector"]
CMD ["--config", "/etc/vector/vector.yaml"]
