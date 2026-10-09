# syntax=docker/dockerfile:1.6

# Stage 1: build the SPA (Node 26, matching local development and CI).
FROM node:26.11.1-alpine@sha256:143494b1da2945f061539253adc65e4f1569ddf07da2d384c022c791a9d90a4a AS build
WORKDIR /app

COPY package.json package-lock.json ./
RUN npm install --global npm@12.2.0 && npm ci

COPY . .

# Baked into the bundle via the Vite `define` (__APP_VERSION__); the release
# workflow passes VERSION to every component build.
ARG VERSION=1.2.1
ARG GIT_COMMIT=unknown
ARG BUILD_DATE=unknown
ENV VERSION=$VERSION \
    GIT_COMMIT=$GIT_COMMIT \
    BUILD_DATE=$BUILD_DATE

# The trailing grep verifies the version string was actually baked into the
# emitted bundle (release builds must be traceable to their tag).
RUN npm run build \
  && grep -R -F -q -- "${VERSION}" dist

# Stage 2: static nginx. Same base pin as deploy/nginx/Dockerfile.nginx —
# bump both digests in lockstep (D16).
FROM docker.io/library/nginx:1.31.6-alpine3.24-slim@sha256:f761b94f2cb9e8e05e2943d5f773609596113ef69b54e2433a996d109a8f78b7 AS nginx-base

# The upstream image is digest pinned, but its Alpine package snapshot can be
# older than the currently fixed security advisories. Keep the refresh in a
# dedicated base stage so the final application stage stays immutable and the
# resulting image contains only the patched package set.
RUN apk update && apk upgrade && rm -rf /var/cache/apk/*

FROM nginx-base

ARG VERSION=1.2.1
ARG GIT_COMMIT=unknown
ARG BUILD_DATE=unknown
LABEL org.opencontainers.image.title="astronomer-frontend" \
      org.opencontainers.image.source="https://github.com/alphabravo-oss/astronomer" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${GIT_COMMIT}" \
      org.opencontainers.image.created="${BUILD_DATE}"

RUN rm -rf /etc/nginx/conf.d /usr/share/nginx/html
COPY nginx.conf /etc/nginx/nginx.conf
COPY --from=build /app/dist /usr/share/nginx/html

# Match Helm frontend.podSecurityContext.runAsUser (listen is 3000).
USER 1001

EXPOSE 3000

HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
  CMD wget --no-verbose --tries=1 --spider http://127.0.0.1:3000/healthz || exit 1

# Bypass /docker-entrypoint.d scripts — they write outside /tmp, which fails
# under readOnlyRootFilesystem.
ENTRYPOINT ["nginx", "-g", "daemon off;"]
