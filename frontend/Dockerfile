# syntax=docker/dockerfile:1.6

# Stage 1: build the SPA (Node 22, matching CI).
FROM node:22-alpine@sha256:16e22a550f3863206a3f701448c45f7912c6896a62de43add43bb9c86130c3e2 AS build
WORKDIR /app

COPY package.json package-lock.json ./
RUN npm ci

COPY . .

# Baked into the bundle via the Vite `define` (__APP_VERSION__); the release
# workflow passes VERSION to every component build.
ARG VERSION=0.3.0
ENV VERSION=$VERSION

# The trailing grep verifies the version string was actually baked into the
# emitted bundle (release builds must be traceable to their tag).
RUN npm run build \
  && grep -R -F -q -- "${VERSION}" dist

# Stage 2: static nginx. Same base pin as deploy/nginx/Dockerfile.nginx —
# bump both digests in lockstep (D16).
FROM nginx:1.29-alpine@sha256:5616878291a2eed594aee8db4dade5878cf7edcb475e59193904b198d9b830de

ARG VERSION=0.3.0
ARG GIT_COMMIT=unknown
ARG BUILD_DATE=unknown
LABEL org.opencontainers.image.title="astronomer-frontend" \
      org.opencontainers.image.source="https://github.com/alphabravo-oss/astronomer" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${GIT_COMMIT}" \
      org.opencontainers.image.created="${BUILD_DATE}"

# Pull Alpine package fixes newer than the base image build (CI scans this
# image with Trivy at HIGH/CRITICAL, ignore-unfixed, exit-code 1).
RUN apk upgrade --no-cache

RUN rm -rf /etc/nginx/conf.d /usr/share/nginx/html
COPY nginx.conf /etc/nginx/nginx.conf
COPY --from=build /app/dist /usr/share/nginx/html

EXPOSE 3000

HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
  CMD wget --no-verbose --tries=1 --spider http://127.0.0.1:3000/healthz || exit 1

# Bypass /docker-entrypoint.d scripts — they write outside /tmp, which fails
# under readOnlyRootFilesystem.
ENTRYPOINT ["nginx", "-g", "daemon off;"]
