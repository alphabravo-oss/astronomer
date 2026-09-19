# syntax=docker/dockerfile:1.6

# Stage 1: build the SPA (Node 24, matching local development and CI).
FROM node:24-alpine@sha256:50c8e8ca1d27439048670df5883f32d57cf81cff6233222c893fd0d9884cbd81 AS build
WORKDIR /app

COPY package.json package-lock.json ./
RUN npm ci

COPY . .

# Baked into the bundle via the Vite `define` (__APP_VERSION__); the release
# workflow passes VERSION to every component build.
ARG VERSION=1.2.0
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
FROM nginx:1.31.0-alpine3.23-slim@sha256:241b0d0fe06250e026e7a35a008d022c9a1d3bec19442d65cc33b84d0b5dd64d AS nginx-base

# The upstream image is digest pinned, but its Alpine package snapshot can be
# older than the currently fixed security advisories. Keep the refresh in a
# dedicated base stage so the final application stage stays immutable and the
# resulting image contains only the patched package set.
RUN apk update && apk upgrade && rm -rf /var/cache/apk/*

FROM nginx-base

ARG VERSION=1.2.0
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
