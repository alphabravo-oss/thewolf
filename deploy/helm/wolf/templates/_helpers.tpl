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
