{{- define "berlin-transit.name" -}}
{{- printf "%s-berlin-transit" .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- define "berlin-transit.selector" -}}
app.kubernetes.io/name: berlin-transit
app.kubernetes.io/instance: {{ .Release.Name | quote }}
{{- end -}}
{{- define "berlin-transit.image" -}}
{{- if .Values.image.digest -}}
{{ .Values.image.repository }}@{{ .Values.image.digest }}
{{- else -}}
{{ .Values.image.repository }}:{{ .Values.image.tag }}
{{- end -}}
{{- end -}}
