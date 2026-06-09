{{/* Expand the name of the chart. */}}
{{- define "pseudonymizer.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/* Fully qualified app name. */}}
{{- define "pseudonymizer.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- $name := default .Chart.Name .Values.nameOverride -}}
{{- if contains $name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{- define "pseudonymizer.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/* Common labels. */}}
{{- define "pseudonymizer.labels" -}}
helm.sh/chart: {{ include "pseudonymizer.chart" . }}
{{ include "pseudonymizer.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{/* Selector labels. */}}
{{- define "pseudonymizer.selectorLabels" -}}
app.kubernetes.io/name: {{ include "pseudonymizer.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "pseudonymizer.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "pseudonymizer.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{/* Component names for the bundled dependencies. */}}
{{- define "pseudonymizer.redis.fullname" -}}
{{- printf "%s-redis" (include "pseudonymizer.fullname" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- define "pseudonymizer.analyzer.fullname" -}}
{{- printf "%s-presidio-analyzer" (include "pseudonymizer.fullname" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- define "pseudonymizer.imageRedactor.fullname" -}}
{{- printf "%s-presidio-image-redactor" (include "pseudonymizer.fullname" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/* Resolved Redis URL: bundled service name or the external url. */}}
{{- define "pseudonymizer.redisURL" -}}
{{- if .Values.redis.enabled -}}
{{- printf "redis://%s:6379/0" (include "pseudonymizer.redis.fullname" .) -}}
{{- else -}}
{{- required "redis.url is required when redis.enabled=false" .Values.redis.url -}}
{{- end -}}
{{- end -}}

{{/* Resolved Presidio Analyzer URL. Bundled service listens on 3000. */}}
{{- define "pseudonymizer.analyzerURL" -}}
{{- if .Values.presidio.analyzer.enabled -}}
{{- printf "http://%s:3000" (include "pseudonymizer.analyzer.fullname" .) -}}
{{- else -}}
{{- required "presidio.analyzer.url is required when presidio.analyzer.enabled=false" .Values.presidio.analyzer.url -}}
{{- end -}}
{{- end -}}

{{/* Resolved Presidio Image Redactor URL. */}}
{{- define "pseudonymizer.imageRedactorURL" -}}
{{- if .Values.presidio.imageRedactor.enabled -}}
{{- printf "http://%s:3000" (include "pseudonymizer.imageRedactor.fullname" .) -}}
{{- else -}}
{{- required "presidio.imageRedactor.url is required when presidio.imageRedactor.enabled=false" .Values.presidio.imageRedactor.url -}}
{{- end -}}
{{- end -}}

{{/* Whether the analyzer needs a mounted config (NER-ORG on OR deny-list set). */}}
{{- define "pseudonymizer.analyzer.needsConfig" -}}
{{- if or .Values.presidio.analyzer.nerOrganization (gt (len .Values.presidio.analyzer.organizations) 0) -}}
true
{{- end -}}
{{- end -}}
