{{- define "documentdb-chart.name" -}}
documentdb-operator
{{- end -}}

{{- define "documentdb-chart.cnpgNamespace" -}}
{{- .Values.cnpg.namespaceOverride | default "cnpg-system" -}}
{{- end -}}