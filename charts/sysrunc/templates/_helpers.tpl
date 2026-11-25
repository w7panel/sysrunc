{{/*
Resolve installation mode once per render using CKM's child-agent identity.
*/}}
{{- define "sysrunc.installMode" -}}
{{- if not (hasKey . "sysruncResolvedInstallMode") -}}
{{- $mode := .Values.installMode | default "auto" -}}
{{- if not (has $mode (list "auto" "host" "nested")) -}}
{{- fail "installMode must be auto, host or nested" -}}
{{- end -}}
{{- if eq $mode "auto" -}}
{{- if not (lookup "v1" "Namespace" "" "kube-system") -}}
{{- fail "installMode=auto requires cluster API access; use explicit host/nested for offline rendering" -}}
{{- end -}}
{{- $mode = "host" -}}
{{- range (lookup "apps/v1" "DaemonSet" "default" "").items -}}
{{- if eq (index (.metadata.labels | default dict) "k3k-agent-pod") "true" -}}
{{- range .spec.template.spec.containers -}}
{{- range .env -}}
{{- if and (eq .name "IS_CHILD") (eq (.value | default "") "true") -}}
{{- $mode = "nested" -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- $_ := set . "sysruncResolvedInstallMode" $mode -}}
{{- end -}}
{{- .sysruncResolvedInstallMode -}}
{{- end -}}

{{/* Expand the name of the chart. */}}
{{- define "sysrunc.name" -}}
{{- .Chart.Name | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "sysrunc.fullname" -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "sysrunc.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "sysrunc.labels" -}}
helm.sh/chart: {{ include "sysrunc.chart" . }}
{{ include "sysrunc.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "sysrunc.selectorLabels" -}}
app.kubernetes.io/name: {{ include "sysrunc.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "sysrunc.serviceAccountName" -}}
{{- printf "%s-installer" (include "sysrunc.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Namespace for namespaced sysrunc installer resources.
*/}}
{{- define "sysrunc.namespace" -}}
{{- .Release.Namespace }}
{{- end }}

{{/*
Installer DaemonSet name.
*/}}
{{- define "sysrunc.daemonSetName" -}}
{{- printf "%s-installer" (include "sysrunc.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
ClusterRole name.
*/}}
{{- define "sysrunc.clusterRoleName" -}}
{{- printf "%s-installer" (include "sysrunc.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
ClusterRoleBinding name.
*/}}
{{- define "sysrunc.clusterRoleBindingName" -}}
{{- printf "%s-installer" (include "sysrunc.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
RuntimeClass name.
*/}}
{{- define "sysrunc.runtimeClassName" -}}
{{- .Values.runtimeClassName | default "sysrunc" }}
{{- end }}

{{/*
Admission backend Deployment name.
*/}}
{{- define "sysrunc.admissionName" -}}
{{- printf "%s-admission" (include "sysrunc.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Admission backend Service name.
*/}}
{{- define "sysrunc.admissionServiceName" -}}
{{- printf "%s-admission" (include "sysrunc.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Admission mutating webhook name.
*/}}
{{- define "sysrunc.admissionWebhookName" -}}
{{- "sysrunc-webhook-mutator" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Admission CA Secret name.
*/}}
{{- define "sysrunc.admissionCASecretName" -}}
{{- "sysrunc-admission-webhook-ca" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Admission TLS Secret name.
*/}}
{{- define "sysrunc.admissionTLSSecretName" -}}
{{- "sysrunc-admission-webhook-tls" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Admission lifecycle Lease name.
*/}}
{{- define "sysrunc.admissionLeaseName" -}}
{{- "sysrunc-admission-webhook-init" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Admission selector labels.
*/}}
{{- define "sysrunc.admissionSelectorLabels" -}}
app.kubernetes.io/name: {{ include "sysrunc.name" . }}-admission
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}
