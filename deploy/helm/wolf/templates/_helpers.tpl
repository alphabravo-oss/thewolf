{{- define "wolf.name" -}}wolf{{- end }}
{{- define "wolf.fullname" -}}{{ .Release.Name }}-wolf{{- end }}
{{- define "wolf.labels" -}}
app.kubernetes.io/name: {{ include "wolf.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}
{{- define "wolf.image" -}}
{{- if .Values.image.digest -}}
{{ .Values.image.repository }}@{{ .Values.image.digest }}
{{- else if .Values.image.allowMutableTag -}}
{{ .Values.image.repository }}:{{ .Values.image.tag | default .Chart.AppVersion }}
{{- else -}}
{{ fail "image.digest is required unless image.allowMutableTag is explicitly true" }}
{{- end -}}
{{- end }}
{{- define "wolf.postgresMode" -}}
{{- $m := .Values.postgres.mode | default "bundled" -}}
{{- if not (or (eq $m "bundled") (eq $m "cnpg") (eq $m "external")) -}}
{{ fail "postgres.mode must be bundled, cnpg, or external" }}
{{- end -}}
{{- $m -}}
{{- end }}
{{- define "wolf.postgresImage" -}}
{{- if contains "@sha256:" .Values.postgres.image -}}
{{ .Values.postgres.image }}
{{- else if .Values.postgres.digest -}}
{{ .Values.postgres.image }}@{{ .Values.postgres.digest }}
{{- else if .Values.postgres.allowMutableImage -}}
{{ .Values.postgres.image }}
{{- else -}}
{{ fail "postgres.digest (or an image@sha256 reference) is required unless postgres.allowMutableImage is explicitly true" }}
{{- end -}}
{{- end }}
{{- define "wolf.cnpgClusterName" -}}
{{ include "wolf.fullname" . }}-cnpg
{{- end }}
{{- define "wolf.postgresSSLMode" -}}
{{- if .Values.postgres.sslmode -}}
{{ .Values.postgres.sslmode }}
{{- else if eq (include "wolf.postgresMode" .) "cnpg" -}}
require
{{- else -}}
disable
{{- end -}}
{{- end }}
{{- define "wolf.postgresDSN" -}}
{{- $mode := include "wolf.postgresMode" . -}}
{{- if eq $mode "external" -}}
{{ required "postgres.external.dsn is required unless postgres.external.existingSecret is set" .Values.postgres.external.dsn }}
{{- else if eq $mode "cnpg" -}}
{{ printf "postgres://%s:%s@%s-rw:5432/%s?sslmode=%s" .Values.postgres.username .Values.postgres.password (include "wolf.cnpgClusterName" .) .Values.postgres.database (include "wolf.postgresSSLMode" .) }}
{{- else -}}
{{ printf "postgres://%s:%s@%s-postgres:5432/%s?sslmode=%s" .Values.postgres.username .Values.postgres.password (include "wolf.fullname" .) .Values.postgres.database (include "wolf.postgresSSLMode" .) }}
{{- end -}}
{{- end }}
{{- define "wolf.dbDSNSecretName" -}}
{{- if and (eq (include "wolf.postgresMode" .) "external") .Values.postgres.external.existingSecret -}}
{{ .Values.postgres.external.existingSecret }}
{{- else -}}
{{ include "wolf.fullname" . }}
{{- end -}}
{{- end }}
{{- define "wolf.dbDSNSecretKey" -}}
{{- if and (eq (include "wolf.postgresMode" .) "external") .Values.postgres.external.existingSecret -}}
{{ .Values.postgres.external.existingSecretKey | default "uri" }}
{{- else -}}
postgres-dsn
{{- end -}}
{{- end }}
{{- define "wolf.postgres5432Egress" -}}
{{- if eq (include "wolf.postgresMode" .) "external" -}}
- ports: [{protocol: TCP, port: 5432}]
{{- else -}}
- to:
    - podSelector:
        matchLabels: {app.kubernetes.io/component: postgres}
  ports: [{protocol: TCP, port: 5432}]
{{- end -}}
{{- end }}
{{- define "wolf.workerServiceAccountName" -}}
{{ .Values.worker.serviceAccountName | default (printf "%s-scan-worker" (include "wolf.fullname" .)) }}
{{- end }}
{{- define "wolf.scannerServiceAccountName" -}}
{{ .Values.scanner.serviceAccountName | default (printf "%s-scanner" (include "wolf.fullname" .)) }}
{{- end }}
{{- define "wolf.scannerReleaseBuilderServiceAccountName" -}}
{{ .Values.scannerRelease.builder.managed.kubernetes.coordinatorServiceAccountName | default (printf "%s-release-builder" (include "wolf.fullname" .)) }}
{{- end }}
{{- define "wolf.scannerReleaseBuildkitServiceAccountName" -}}
{{ .Values.scannerRelease.builder.managed.kubernetes.buildkitServiceAccountName | default (printf "%s-release-buildkit" (include "wolf.fullname" .)) }}
{{- end }}
{{- define "wolf.scannerReleaseSignerServiceAccountName" -}}
{{ .Values.scannerRelease.signing.serviceAccountName | default (printf "%s-release-signer" (include "wolf.fullname" .)) }}
{{- end }}
{{- define "wolf.scannerReleaseFixedServiceAccountName" -}}
{{ .Values.scannerRelease.builder.managed.adapters.fixed.serviceAccountName | default (printf "%s-release-fixed" (include "wolf.fullname" .)) }}
{{- end }}
{{- define "wolf.scannerReleaseQualityServiceAccountName" -}}
{{ .Values.scannerRelease.builder.managed.adapters.quality.serviceAccountName | default (printf "%s-release-quality" (include "wolf.fullname" .)) }}
{{- end }}
{{- define "wolf.scannerReleaseIntegrationServiceAccountName" -}}
{{ .Values.scannerRelease.builder.managed.adapters.integration.serviceAccountName | default (printf "%s-release-integration" (include "wolf.fullname" .)) }}
{{- end }}
