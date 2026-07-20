{{/*
Expand the name of the chart.
*/}}
{{- define "network-enforcer.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "network-enforcer.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "network-enforcer.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "network-enforcer.labels" -}}
helm.sh/chart: {{ include "network-enforcer.chart" . }}
{{ include "network-enforcer.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "network-enforcer.selectorLabels" -}}
app.kubernetes.io/name: {{ include "network-enforcer.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Set OTEL endpoint (defaults to controller OTLP service in release namespace)
*/}}
{{- define "network-enforcer.cniwatcher.otelEndpoint" -}}
{{- if .Values.cniwatcher.otelEndpoint -}}
{{- .Values.cniwatcher.otelEndpoint -}}
{{- else -}}
{{- printf "%s-otlp.%s.svc.cluster.local:4317" (include "network-enforcer.fullname" .) .Release.Namespace -}}
{{- end -}}
{{- end -}}

{{/*
CNI-specific volume mounts for cniwatcher
*/}}
{{- define "network-enforcer.cniwatcher.volumeMounts" -}}
{{- if eq .Values.cniwatcher.cniType "cilium" }}
- name: hubble-sock
  mountPath: /var/run/cilium
{{- else if eq .Values.cniwatcher.cniType "calico" }}
- name: goldmane-key-pair-volume
  mountPath: /etc/goldmane/certs
  readOnly: true
{{- else if eq .Values.cniwatcher.cniType "flannel" }}
- name: flannel-ulog
  mountPath: /var/log/ulog
  readOnly: true
{{- else if eq .Values.cniwatcher.cniType "aws-vpc" }}
- name: aws-eni-logs
  mountPath: /var/log/aws-routed-eni
  readOnly: true
{{- end }}
{{- if .Values.cniwatcher.mtls.enabled }}
- name: cniwatcher-mtls-certs
  mountPath: {{ .Values.cniwatcher.mtls.certDir }}
  readOnly: true
{{- end }}
{{- end -}}

{{/*
CNI-specific volumes for cniwatcher
*/}}
{{- define "network-enforcer.cniwatcher.volumes" -}}
{{- if eq .Values.cniwatcher.cniType "cilium" }}
- name: hubble-sock
  hostPath:
    path: /var/run/cilium
{{- else if eq .Values.cniwatcher.cniType "calico" }}
- name: goldmane-key-pair-volume
  secret:
    secretName: cniwatcher-goldmane-key-pair
{{- else if eq .Values.cniwatcher.cniType "flannel" }}
- name: flannel-ulog
  hostPath:
    path: /var/log/ulog
    type: Directory
{{- else if eq .Values.cniwatcher.cniType "aws-vpc" }}
- name: aws-eni-logs
  hostPath:
    path: /var/log/aws-routed-eni
    type: Directory
{{- end }}
{{- if .Values.cniwatcher.mtls.enabled }}
- name: cniwatcher-mtls-certs
  projected:
    sources:
      - secret:
          name: cniwatcher-mtls-certs
          items:
            - key: tls.crt
              path: tls.crt
            - key: tls.key
              path: tls.key
            - key: ca.crt
              path: ca.crt
{{- end }}
{{- end -}}
