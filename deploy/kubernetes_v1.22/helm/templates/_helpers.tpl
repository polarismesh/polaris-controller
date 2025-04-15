{{/* vim: set filetype=mustache: */}}
{{/*
Expand the name of the chart.
*/}}
{{- define "polaris-controller.name" -}}
{{- default .Chart.Name .Values.controller.name | trunc 63 | trimSuffix "-" -}}
{{- end -}}


{{/*
Polaris controller deployment labels
*/}}
{{- define "polaris-controller.controller.labels" -}}
qcloud-app: polaris-controller
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}


{{/*
Get specific image for controller
*/}}
{{- define "polaris-controller.controller.image" -}}
{{- printf "%s:%s" .Values.controller.image.repo .Values.controller.image.tag -}}
{{- end -}}

{{/*
Get specific image for sidecar
*/}}
{{- define "polaris-controller.sidecar.image" -}}
{{- printf "%s:%s" .Values.sidecar.image.repo .Values.sidecar.image.tag -}}
{{- end -}}


{{/*
Get specific image for sidecar init container
*/}}
{{- define "polaris-controller.sidecar.init.image" -}}
{{- printf "%s:%s" .Values.sidecar.init.image.repo .Values.sidecar.init.image.tag -}}
{{- end -}}

{{/*
Get specific image for sidecar init container
*/}}
{{- define "polaris-controller.sidecar.envoy.image" -}}
{{- printf "%s:%s" .Values.sidecar.envoy.image.repo .Values.sidecar.envoy.image.tag -}}
{{- end -}}

{{/*
Get specific image for javaagent init container
*/}}
{{- define "polaris-controller.sidecar.javaagent.image" -}}
{{- printf "%s:%s" .Values.sidecar.javaagent.image.repo .Values.sidecar.javaagent.image.tag -}}
{{- end -}}

{{/*
Get specific image for sidecar init container
*/}}
{{- define "polaris-controller.sidecar.envoy_init.image" -}}
{{- printf "%s:%s" .Values.sidecar.envoy_builder.image.repo .Values.sidecar.envoy_builder.image.tag -}}
{{- end -}}

{{/*
Get specific image for sidecar init container
*/}}
{{- define "polaris-controller.sidecar.istio.image" -}}
{{- printf "%s:%s" .Values.sidecar.istio.image.repo .Values.sidecar.istio.image.tag -}}
{{- end -}}


{{/*
Create a default fully qualified controller name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
*/}}
{{- define "polaris-controller.controller.fullname" -}}
{{- printf "%s-%s" .Release.Name .Values.controller.name | trunc 63 | trimSuffix "-" -}}
{{- end -}}


{{/*
Selector labels
*/}}
{{- define "polaris-controller.controller.selectorLabels" -}}
app.kubernetes.io/name: {{ include "polaris-controller.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app: sidecar-injector
{{- end -}}

