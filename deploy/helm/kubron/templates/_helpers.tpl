{{/* Имя чарта */}}
{{- define "kubron.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/* Полное имя релиза */}}
{{- define "kubron.fullname" -}}
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

{{- define "kubron.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/* Метки селектора: не меняются между обновлениями (иначе Deployment не обновить) */}}
{{- define "kubron.selectorLabels" -}}
app.kubernetes.io/name: {{ include "kubron.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{- define "kubron.labels" -}}
helm.sh/chart: {{ include "kubron.chart" . }}
{{ include "kubron.selectorLabels" . }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{- define "kubron.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "kubron.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/* Образ: digest важнее тега, тег по умолчанию — appVersion */}}
{{- define "kubron.image" -}}
{{- if .Values.image.digest }}
{{- printf "%s@%s" .Values.image.repository .Values.image.digest }}
{{- else }}
{{- printf "%s:%s" .Values.image.repository (default .Chart.AppVersion .Values.image.tag) }}
{{- end }}
{{- end }}

{{/* Имя Secret с ключом шифрования и строкой подключения */}}
{{- define "kubron.secretName" -}}
{{- if .Values.secrets.create }}
{{- printf "%s-secrets" (include "kubron.fullname" .) }}
{{- else }}
{{- required "secrets.existingSecret is required: create a Secret with the encryption key and the database URL (docs/deploy.md), or set secrets.create=true for development only" .Values.secrets.existingSecret }}
{{- end }}
{{- end }}

{{/* Доверять X-Forwarded-Proto: null — автоматически (ingress с TLS), иначе значение как есть */}}
{{- define "kubron.trustProxy" -}}
{{- if kindIs "invalid" .Values.kubron.trustProxy }}
{{- if and .Values.ingress.enabled .Values.ingress.tls }}1{{ else }}0{{ end }}
{{- else }}
{{- if .Values.kubron.trustProxy }}1{{ else }}0{{ end }}
{{- end }}
{{- end }}

{{/* Токен ServiceAccount монтируется только в режиме in-cluster */}}
{{- define "kubron.automountToken" -}}
{{- if .Values.inCluster.enabled }}true{{ else }}false{{ end }}
{{- end }}

{{/* Имя кластерных объектов (ClusterRole/Binding): с namespace, иначе одноимённые релизы конфликтуют */}}
{{- define "kubron.clusterRoleName" -}}
{{- printf "%s-%s" (include "kubron.fullname" .) .Release.Namespace | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Допустимые значения заголовка Host (KUBRON_ALLOWED_HOSTS), через запятую без повторов: хосты ingress.hosts при
ingress.enabled, «*.pf.<portForward.baseDomain>» при port-forward и kubron.allowedHosts. Пусто — переменная не задаётся.
*/}}
{{- define "kubron.allowedHosts" -}}
{{- $hosts := list }}
{{- if .Values.ingress.enabled }}
{{- range .Values.ingress.hosts }}
{{- $hosts = append $hosts .host }}
{{- end }}
{{- end }}
{{- with .Values.portForward.baseDomain }}
{{- $hosts = append $hosts (printf "*.pf.%s" .) }}
{{- end }}
{{- range .Values.kubron.allowedHosts }}
{{- $hosts = append $hosts . }}
{{- end }}
{{- join "," (uniq $hosts) }}
{{- end }}

{{/*
Адресаты правила egress (список для поля to). Аргументы: target — настройки правила (to, cidrs, а для открытого режима
except и allowCluster), open — true: без to и cidrs разрешить любые адреса, кроме except; false: вернуть пусто
(правило без адресатов, то есть «куда угодно»). Приоритет: to > cidrs > любые адреса, кроме except.
Список собирается данными и выводится toYaml: IPv4 и IPv6 исключения раскладываются по своим ipBlock.
*/}}
{{- define "kubron.egressTo" -}}
{{- $t := .target }}
{{- if $t.to }}
{{- toYaml $t.to }}
{{- else if $t.cidrs }}
{{- $dests := list }}
{{- range $t.cidrs }}
{{- $dests = append $dests (dict "ipBlock" (dict "cidr" .)) }}
{{- end }}
{{- toYaml $dests }}
{{- else if .open }}
{{- $v4 := list }}
{{- $v6 := list }}
{{- range $t.except }}
{{- if contains ":" . }}
{{- $v6 = append $v6 . }}
{{- else }}
{{- $v4 = append $v4 . }}
{{- end }}
{{- end }}
{{- $b4 := dict "cidr" "0.0.0.0/0" }}
{{- if $v4 }}
{{- $_ := set $b4 "except" $v4 }}
{{- end }}
{{- $b6 := dict "cidr" "::/0" }}
{{- if $v6 }}
{{- $_ := set $b6 "except" $v6 }}
{{- end }}
{{- $dests := list (dict "ipBlock" $b4) (dict "ipBlock" $b6) }}
{{- if $t.allowCluster }}
{{- $dests = append $dests (dict "namespaceSelector" (dict)) }}
{{- end }}
{{- toYaml $dests }}
{{- end }}
{{- end }}
