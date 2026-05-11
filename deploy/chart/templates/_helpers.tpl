{{/*
Chart name (truncated to 63 chars per DNS-1123 spec).
*/}}
{{- define "astronomer.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Fully qualified release name. Combines release name + chart name,
unless fullnameOverride is provided.
*/}}
{{- define "astronomer.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else -}}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else -}}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Chart label "<name>-<version>" with non-DNS chars stripped.
*/}}
{{- define "astronomer.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels applied to every resource.
*/}}
{{- define "astronomer.labels" -}}
helm.sh/chart: {{ include "astronomer.chart" . }}
{{ include "astronomer.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: astronomer
{{- end }}

{{/*
Selector labels (subset used in Service/Deployment selectors).
*/}}
{{- define "astronomer.selectorLabels" -}}
app.kubernetes.io/name: {{ include "astronomer.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Component labels: extends labels with app.kubernetes.io/component.
Usage:
  {{- include "astronomer.componentLabels" (dict "context" $ "component" "server") | nindent 4 }}
*/}}
{{- define "astronomer.componentLabels" -}}
{{ include "astronomer.labels" .context }}
app.kubernetes.io/component: {{ .component }}
{{- end }}

{{- define "astronomer.componentSelectorLabels" -}}
{{ include "astronomer.selectorLabels" .context }}
app.kubernetes.io/component: {{ .component }}
{{- end }}

{{/*
ServiceAccount name to use.
*/}}
{{- define "astronomer.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "astronomer.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end }}

{{/*
Image reference helper. Pass dict { context: $, component: "server"|"worker"|"agent"|"migrate" }.
Honours .Values.image.registry as an optional global prefix.
*/}}
{{- define "astronomer.image" -}}
{{- $reg := .context.Values.image.registry | default "" -}}
{{- $img := index .context.Values.image .component -}}
{{- if $reg -}}
{{ printf "%s/%s:%s" $reg $img.repository $img.tag }}
{{- else -}}
{{ printf "%s:%s" $img.repository $img.tag }}
{{- end -}}
{{- end }}

{{/*
Computed DATABASE_URL — uses chart-managed Postgres unless overridden.
*/}}
{{- define "astronomer.databaseURL" -}}
{{- if .Values.postgres.external.dsnSecretRef.name -}}
{{- "" -}}
{{- else if .Values.postgres.external.dsn -}}
{{- .Values.postgres.external.dsn -}}
{{- else if .Values.config.databaseURL -}}
{{- .Values.config.databaseURL -}}
{{- else if not .Values.postgres.bundled.enabled -}}
{{- fail "postgres.bundled.enabled=false requires postgres.external.dsn, postgres.external.dsnSecretRef, or config.databaseURL" -}}
{{- else -}}
{{- printf "postgres://%s:%s@%s-postgres:%d/%s?sslmode=disable" .Values.postgres.user .Values.postgres.password (include "astronomer.fullname" .) (int .Values.postgres.port) .Values.postgres.database -}}
{{- end -}}
{{- end }}

{{/*
Computed REDIS_URL.
*/}}
{{- define "astronomer.redisURL" -}}
{{- if .Values.redis.external.address -}}
{{- $scheme := ternary "rediss" "redis" .Values.redis.external.tls -}}
{{- $auth := "" -}}
{{- if .Values.redis.external.passwordSecretRef.name -}}
{{- $auth = ":$(REDIS_PASSWORD)@" -}}
{{- end -}}
{{- printf "%s://%s%s/%d" $scheme $auth .Values.redis.external.address (int .Values.redis.external.database) -}}
{{- else if .Values.config.redisURL -}}
{{- .Values.config.redisURL -}}
{{- else if not .Values.redis.bundled.enabled -}}
{{- fail "redis.bundled.enabled=false requires redis.external.address or config.redisURL" -}}
{{- else -}}
{{- printf "redis://%s-redis:%d/0" (include "astronomer.fullname" .) (int .Values.redis.port) -}}
{{- end -}}
{{- end }}

{{- define "astronomer.postgresBundledEnabled" -}}
{{- .Values.postgres.bundled.enabled -}}
{{- end }}

{{- define "astronomer.redisBundledEnabled" -}}
{{- .Values.redis.bundled.enabled -}}
{{- end }}

{{/*
Default pod anti-affinity for HA components. Keeps replicas apart when the
operator hasn't supplied a stronger affinity policy at the chart root.
*/}}
{{- define "astronomer.componentAffinity" -}}
{{- if .enabled }}
podAntiAffinity:
  preferredDuringSchedulingIgnoredDuringExecution:
    - weight: 100
      podAffinityTerm:
        topologyKey: kubernetes.io/hostname
        labelSelector:
          matchLabels:
            {{- include "astronomer.componentSelectorLabels" (dict "context" .context "component" .component) | nindent 12 }}
{{- end }}
{{- end }}

{{/*
Default topology spread constraints for HA components. These target zones when
available and fall back to scheduler best-effort semantics.
*/}}
{{- define "astronomer.componentTopologySpread" -}}
{{- if .enabled }}
- maxSkew: {{ .maxSkew }}
  topologyKey: topology.kubernetes.io/zone
  whenUnsatisfiable: {{ .whenUnsatisfiable }}
  labelSelector:
    matchLabels:
      {{- include "astronomer.componentSelectorLabels" (dict "context" .context "component" .component) | nindent 6 }}
{{- end }}
{{- end }}

{{/*
TLS helpers — single source of truth that gateway.* and ingress.* templates
read from. The mode selector is `tls.source` with values:
  none | selfSigned | letsEncrypt | secret
*/}}

{{- define "astronomer.tls.source" -}}
{{- default "none" .Values.tls.source -}}
{{- end }}

{{- define "astronomer.tls.enabled" -}}
{{- if ne (include "astronomer.tls.source" .) "none" -}}true{{- end -}}
{{- end }}

{{- define "astronomer.tls.secretName" -}}
{{- if .Values.tls.secretName -}}
{{- .Values.tls.secretName -}}
{{- else -}}
{{- printf "%s-tls" (include "astronomer.fullname" .) -}}
{{- end -}}
{{- end }}

{{/*
True when the chart should template a cert-manager Issuer + Certificate.
Both selfSigned and letsEncrypt modes go through cert-manager; secret mode
is BYO and creates nothing; none mode disables TLS entirely.
*/}}
{{- define "astronomer.tls.usesCertManager" -}}
{{- $src := include "astronomer.tls.source" . -}}
{{- if or (eq $src "selfSigned") (eq $src "letsEncrypt") -}}true{{- end -}}
{{- end }}

{{- define "astronomer.tls.issuerName" -}}
{{- printf "%s-tls" (include "astronomer.fullname" .) -}}
{{- end }}

{{/*
Solver ingress class for the letsEncrypt HTTP-01 path. Explicit override wins;
otherwise we use whatever class the public listener (gateway or ingress) is on.
*/}}
{{- define "astronomer.tls.solverIngressClass" -}}
{{- if .Values.tls.letsEncrypt.ingressClassName -}}
{{- .Values.tls.letsEncrypt.ingressClassName -}}
{{- else if .Values.gateway.enabled -}}
{{- .Values.gateway.className -}}
{{- else -}}
{{- .Values.ingress.className -}}
{{- end -}}
{{- end }}

{{/*
Primary hostname for the Certificate's commonName when commonName isn't set
explicitly. Prefers gateway.hosts[0], falls back to ingress.host.
*/}}
{{- define "astronomer.tls.primaryHost" -}}
{{- if and .Values.gateway.enabled (gt (len .Values.gateway.hosts) 0) -}}
{{- index .Values.gateway.hosts 0 -}}
{{- else -}}
{{- .Values.ingress.host -}}
{{- end -}}
{{- end }}

{{/*
DNS names list for the Certificate. Unions gateway.hosts and ingress.host
(deduplicated) so a single Cert covers both code paths if both are enabled.
*/}}
{{- define "astronomer.tls.dnsNames" -}}
{{- $names := list -}}
{{- if .Values.gateway.enabled -}}
{{- range .Values.gateway.hosts -}}
{{- $names = append $names . -}}
{{- end -}}
{{- end -}}
{{- if and .Values.ingress.enabled .Values.ingress.host -}}
{{- if not (has .Values.ingress.host $names) -}}
{{- $names = append $names .Values.ingress.host -}}
{{- end -}}
{{- end -}}
{{- range ($names | uniq) }}
- {{ . | quote }}
{{- end -}}
{{- end }}

{{/*
astronomer.validateInputs runs on every render (any env), catching misconfig
that produces silent-noop rather than fail-fast. Currently checks:
  - additionalTrustedCAs.enabled=true requires existingSecret (otherwise the
    server volume mount template silently skips and the CA bundle never lands
    in the pod, leaving private-CA-signed upstream calls broken).
Returns "" so callers can drop the result without it appearing in the
rendered manifest.
*/}}
{{- define "astronomer.validateInputs" -}}
  {{- $errs := list }}
  {{- if and .Values.tls.additionalTrustedCAs.enabled (not .Values.tls.additionalTrustedCAs.existingSecret) }}
    {{- $errs = append $errs "  - tls.additionalTrustedCAs.enabled=true requires tls.additionalTrustedCAs.existingSecret (a Secret with key tls.crt)" }}
  {{- end }}
  {{- if gt (len $errs) 0 }}
    {{- $msg := printf "\n\nAstronomer chart input validation failed:\n%s" (join "\n" $errs) }}
    {{- fail $msg }}
  {{- end }}
{{- end }}

{{/*
astronomer.requireProductionInputs is consumed by configmap.yaml to fail the
render when env=production but mandatory production knobs are missing. Helm's
`fail` builtin aborts the whole release with a single human-readable message
rather than letting the user discover three minutes later that the server
pod is CrashLooping because of an empty Fernet key.
Returns "" so callers can drop the result without it appearing in the
rendered manifest.
*/}}
{{- define "astronomer.requireProductionInputs" -}}
  {{- if eq (default "" .Values.config.env) "production" }}
    {{- $errs := list }}
    {{- if not (or .Values.postgres.external.dsn .Values.postgres.external.dsnSecretRef.name) }}
      {{- $errs = append $errs "  - postgres.external.dsn or postgres.external.dsnSecretRef.name must be set when config.env=production (bundled Postgres is not a production posture)" }}
    {{- end }}
    {{- if .Values.postgres.bundled.enabled }}
      {{- $errs = append $errs "  - postgres.bundled.enabled must be false when config.env=production" }}
    {{- end }}
    {{- if .Values.redis.bundled.enabled }}
      {{- $errs = append $errs "  - redis.bundled.enabled must be false when config.env=production" }}
    {{- end }}
    {{- if not .Values.redis.external.address }}
      {{- $errs = append $errs "  - redis.external.address must be set when config.env=production" }}
    {{- end }}
    {{- if not .Values.config.serverURL }}
      {{- $errs = append $errs "  - config.serverURL must be set to the external https URL of this install" }}
    {{- end }}
    {{- if not (and .Values.gateway.enabled (gt (len .Values.gateway.hosts) 0)) }}
      {{- $errs = append $errs "  - gateway.enabled=true with at least one gateway.hosts entry is required" }}
    {{- end }}
    {{- $tlsSrc := include "astronomer.tls.source" . }}
    {{- if eq $tlsSrc "none" }}
      {{- $errs = append $errs "  - tls.source must be one of: selfSigned, letsEncrypt, secret (got 'none')" }}
    {{- end }}
    {{- if eq $tlsSrc "letsEncrypt" }}
      {{- if not .Values.tls.letsEncrypt.email }}
        {{- $errs = append $errs "  - tls.letsEncrypt.email must be set when tls.source=letsEncrypt" }}
      {{- end }}
    {{- end }}
    {{- if eq (default "" .Values.secrets.secretKey) "local-dev-secret-key-change-in-production" }}
      {{- $errs = append $errs "  - secrets.secretKey is still the chart's known dev value — replace it (JWT signing key)" }}
    {{- end }}
    {{- if eq (default "" .Values.secrets.encryptionKey) "RX3rwYkQNmaSq4_UmGs7sPXONIjnB-M6q0gZtB79vQA=" }}
      {{- $errs = append $errs "  - secrets.encryptionKey is still the chart's known dev Fernet key — replace it" }}
    {{- end }}
    {{- if not .Values.secrets.secretKey }}
      {{- $errs = append $errs "  - secrets.secretKey is empty (required)" }}
    {{- end }}
    {{- if not .Values.secrets.encryptionKey }}
      {{- $errs = append $errs "  - secrets.encryptionKey is empty (required Fernet key)" }}
    {{- end }}
    {{- if gt (len $errs) 0 }}
      {{- $msg := printf "\n\nAstronomer production preflight failed:\n%s\n\nSee deploy/chart/README.md and deploy/chart/values-production.yaml for the expected wiring." (join "\n" $errs) }}
      {{- fail $msg }}
    {{- end }}
  {{- end }}
{{- end }}
