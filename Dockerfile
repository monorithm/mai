FROM astral/uv:0.13.7 AS uv

FROM postgres:18.6-alpine AS build
RUN apk add --no-cache python3
COPY --from=uv /uv /usr/local/bin/uv
ENV UV_LINK_MODE=copy UV_PYTHON_DOWNLOADS=never UV_PROJECT_ENVIRONMENT=/opt/mai/.venv
WORKDIR /opt/mai
COPY pyproject.toml uv.lock ./
RUN uv sync --frozen --no-dev --no-install-project --python /usr/bin/python3
COPY src ./src
RUN uv sync --frozen --no-dev --no-editable --python /usr/bin/python3

FROM postgres:18.6-alpine
RUN apk add --no-cache tini git python3 openssl \
      postfix postfix-pgsql \
      dovecot dovecot-lmtpd dovecot-pgsql \
 && adduser -D -u 10001 mai \
 && (deluser vmail 2>/dev/null || true) && (delgroup vmail 2>/dev/null || true) \
 && addgroup -g 10002 vmail && adduser -D -u 10002 -G vmail -h /data/mail -s /sbin/nologin vmail \
 && cp -a /etc/postfix /opt/postfix-dist && cp -a /etc/dovecot /opt/dovecot-dist \
 && rm -rf /var/spool/postfix && mkdir -p /var/lib/mai/spool
COPY --from=build /opt/mai/.venv /opt/mai/.venv
COPY src /opt/mai/src
COPY etc/ /opt/mai/etc/
COPY LICENSE NOTICE /opt/mai/
ENV PATH=/opt/mai/.venv/bin:$PATH \
    MAI_ETC=/opt/mai/etc \
    DATABASE_BACKEND=postgres \
    PYTHONUNBUFFERED=1
EXPOSE 8080 1143 1587 1993
VOLUME /data
HEALTHCHECK --interval=30s --timeout=10s --start-period=60s CMD ["mai", "health"]
ENTRYPOINT ["/sbin/tini", "--"]
CMD ["mai", "run"]
