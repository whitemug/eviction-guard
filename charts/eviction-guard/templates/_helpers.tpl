{{- define "eviction-guard.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "eviction-guard.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- printf "%s" $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}

{{- define "eviction-guard.webhookServiceName" -}}
{{- printf "%s-webhook" (include "eviction-guard.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "eviction-guard.webhookSecretName" -}}
{{- printf "%s-webhook-certs" (include "eviction-guard.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "eviction-guard.labels" -}}
helm.sh/chart: {{ .Chart.Name }}-{{ .Chart.Version | replace "+" "_" }}
app.kubernetes.io/name: {{ include "eviction-guard.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}
