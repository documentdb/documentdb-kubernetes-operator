{{- define "documentdb-chart.name" -}}
documentdb-operator
{{- end -}}

{{/*
documentdb.imageRef composes a container image reference from a registry prefix,
a repository, and an optional tag, following the canonical Docker/containerd
reference rule for deciding whether the repository already carries a registry
host: the first path segment is treated as a registry host when it contains a
"." or ":" (port), or equals "localhost". In that case the repository is used
verbatim and the registry prefix is ignored (this is also the per-component
per-registry override path). Otherwise the registry prefix is prepended.

The repository must be a non-empty host/path only. Rendering fails (naming the
offending setting via "name") when the repository is empty, when it already
carries a tag or digest (a ":" or "@" in the final path segment), or when the
registry prefix would be needed but is empty. The tag is always supplied via the
component's tag field (or documentDbVersion for the data-plane images) rather
than embedded in the repository.

Usage:
  {{ include "documentdb.imageRef" (dict "name" "image.foo.repository" "registry" .Values.image.registry "repo" $repo "tag" $tag) }}
Pass tag "" to compose a bare repository (host+path, no tag).
*/}}
{{- define "documentdb.imageRef" -}}
{{- $name := .name | default "image repository" -}}
{{- if not .repo -}}
{{- fail (printf "%s is empty; set it to a host/path (with image.registry) or a full host reference" $name) -}}
{{- end -}}
{{- $lastSeg := (splitList "/" .repo) | last -}}
{{- if or (contains ":" $lastSeg) (contains "@" $lastSeg) -}}
{{- fail (printf "%s value %q must be a host/path without a tag or digest; set the tag via the component's tag field (or documentDbVersion for data-plane images)" $name .repo) -}}
{{- end -}}
{{- $first := (splitList "/" .repo) | first -}}
{{- $full := .repo -}}
{{- if not (or (contains "." $first) (contains ":" $first) (eq $first "localhost")) -}}
{{- if not .registry -}}
{{- fail (printf "image.registry is empty but %s (%q) is a relative path; set image.registry or use a full host reference in the repository" $name .repo) -}}
{{- end -}}
{{- $full = printf "%s/%s" .registry .repo -}}
{{- end -}}
{{- if .tag -}}
{{- printf "%s:%s" $full .tag -}}
{{- else -}}
{{- $full -}}
{{- end -}}
{{- end -}}

{{/*
documentdb.extraImageRef composes an optional override image reference for the
"extra" images (otel collector, postgres) that carry no fallback tag source.
Unlike the first-party images (whose tag defaults to Chart.AppVersion or
documentDbVersion), these have no default tag, so a partial override is always a
mistake: a repository without a tag would leak a floating ":latest", and a tag
without a repository would be silently dropped. This helper therefore enforces
that the component's repository and tag are set together (both or neither):

  - both empty  -> returns "" (caller omits the env var; the operator uses its
                   compiled-in default for otel, or defers to CNPG for postgres)
  - both set    -> returns the composed "registry/repo:tag" reference
  - exactly one -> rendering fails, naming both settings

Usage:
  {{ include "documentdb.extraImageRef" (dict "registry" .Values.image.registry "repo" $repo "tag" $tag "repoName" "image.otelCollector.repository" "tagName" "image.otelCollector.tag") }}
*/}}
{{- define "documentdb.extraImageRef" -}}
{{- if and .repo (not .tag) -}}
{{- fail (printf "%s is set but %s is empty; pin a tag (%s and %s must be set together)" .repoName .tagName .repoName .tagName) -}}
{{- end -}}
{{- if and .tag (not .repo) -}}
{{- fail (printf "%s is set but %s is empty; set the repository (%s and %s must be set together)" .tagName .repoName .repoName .tagName) -}}
{{- end -}}
{{- if .repo -}}
{{- include "documentdb.imageRef" (dict "name" .repoName "registry" .registry "repo" .repo "tag" .tag) -}}
{{- end -}}
{{- end -}}

{{/*
documentdb.pullPolicy validates and echoes an image pull policy. An empty value
yields an empty string (the caller omits the setting and the consumer applies
its own default); a non-empty value must be one of Always, IfNotPresent, or
Never, otherwise rendering fails naming the offending setting via "name".

Usage:
  {{ include "documentdb.pullPolicy" (dict "name" "image.gateway.pullPolicy" "policy" .Values.image.gateway.pullPolicy) }}
*/}}
{{- define "documentdb.pullPolicy" -}}
{{- if .policy -}}
{{- if not (has .policy (list "Always" "IfNotPresent" "Never")) -}}
{{- fail (printf "%s must be one of Always, IfNotPresent, Never (got %q)" .name .policy) -}}
{{- end -}}
{{- .policy -}}
{{- end -}}
{{- end -}}
{{/*
documentdb.proxySecretName is the name of the Secret that holds the outbound
proxy routing configuration (HTTP_PROXY / HTTPS_PROXY / NO_PROXY). It is created
in the operator namespace when Azure.proxySettings.isProxyEnabled is true.
*/}}
{{- define "documentdb.proxySecretName" -}}
documentdb-proxy-config
{{- end -}}

{{/*
documentdb.noProxy composes the effective NO_PROXY value: the user-supplied
Azure.proxySettings.noProxy (optional) followed by a standard in-cluster bypass
list so that kube-apiserver, localhost Postgres scrapes and the in-pod OTLP
receiver never route through the proxy. Duplicate/empty entries are dropped and
the result is comma-joined.
*/}}
{{- define "documentdb.noProxy" -}}
{{- $defaults := list "localhost" "127.0.0.1" "::1" ".svc" ".svc.cluster.local" "kubernetes.default.svc" "10.0.0.0/8" "172.16.0.0/12" "192.168.0.0/16" -}}
{{- $user := list -}}
{{- with .Values.Azure.proxySettings.noProxy -}}
{{- $user = regexSplit "\\s*,\\s*" (trim .) -1 -}}
{{- end -}}
{{- $seen := dict -}}
{{- $out := list -}}
{{- range (concat $user $defaults) -}}
{{- $e := trim . -}}
{{- if and $e (not (hasKey $seen $e)) -}}
{{- $_ := set $seen $e true -}}
{{- $out = append $out $e -}}
{{- end -}}
{{- end -}}
{{- join "," $out -}}
{{- end -}}
