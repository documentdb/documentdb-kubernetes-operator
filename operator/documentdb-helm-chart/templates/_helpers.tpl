{{/*
Chart name (overridable via .Values.nameOverride). Used as
app.kubernetes.io/name across the chart.
*/}}
{{- define "documentdb-operator.name" -}}
{{- default "documentdb-operator" .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Chart label value (name + version, sanitized).
*/}}
{{- define "documentdb-operator.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Operator-namespace resolution: honor the chart's legacy .Values.namespace
override and fall back to .Release.Namespace.
*/}}
{{- define "documentdb-operator.namespace" -}}
{{- default .Release.Namespace .Values.namespace -}}
{{- end -}}

{{/*
Common labels applied to every resource. Wraps selectorLabels so the two
stay in sync.

Usage:
  {{- include "documentdb-operator.labels" (dict "root" $ "component" "operator") | nindent 4 }}
*/}}
{{- define "documentdb-operator.labels" -}}
helm.sh/chart: {{ include "documentdb-operator.chart" .root }}
{{ include "documentdb-operator.selectorLabels" . }}
{{- if .root.Chart.AppVersion }}
app.kubernetes.io/version: {{ .root.Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .root.Release.Service }}
{{- end -}}

{{/*
Selector labels. Includes app.kubernetes.io/component so each Deployment in
this chart (operator, sidecar-injector, wal-replica) gets a distinct,
immutable selector. Previously the operator Deployment used
`app: {{ .Release.Name }}`, which coupled the immutable selector to the
release name.

Usage:
  {{- include "documentdb-operator.selectorLabels" (dict "root" $ "component" "operator") | nindent 6 }}
*/}}
{{- define "documentdb-operator.selectorLabels" -}}
app.kubernetes.io/name: {{ include "documentdb-operator.name" .root }}
app.kubernetes.io/instance: {{ .root.Release.Name }}
app.kubernetes.io/component: {{ .component }}
{{- end -}}

{{/*
Service account name. Defaults to the stable name `documentdb-operator`.
Override via .Values.serviceAccount.name if you need a fixed external name
(e.g., an existing workload-identity binding).
*/}}
{{- define "documentdb-operator.serviceAccountName" -}}
{{- default "documentdb-operator" .Values.serviceAccount.name -}}
{{- end -}}
