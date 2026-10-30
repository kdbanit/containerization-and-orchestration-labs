{{/*
Lab 4 Part 1/4: placement — topologySpreadConstraints (chosen) or podAntiAffinity.
Enable via .Values.spreading.enabled on the target Deployment (api).
*/}}
{{- define "shop.placement" -}}
{{- if .Values.spreading.enabled }}
{{- if eq .Values.spreading.mechanism "topologySpreadConstraints" }}
topologySpreadConstraints:
  - maxSkew: {{ .Values.spreading.maxSkew | default 1 }}
    topologyKey: kubernetes.io/hostname
    whenUnsatisfiable: {{ .Values.spreading.whenUnsatisfiable | default "DoNotSchedule" }}
    labelSelector:
      matchLabels:
        app.kubernetes.io/name: {{ .appName }}
        app.kubernetes.io/instance: {{ .Release.Name }}
{{- else if eq .Values.spreading.mechanism "podAntiAffinity" }}
affinity:
  podAntiAffinity:
    requiredDuringSchedulingIgnoredDuringExecution:
      - labelSelector:
          matchLabels:
            app.kubernetes.io/name: {{ .appName }}
            app.kubernetes.io/instance: {{ .Release.Name }}
        topologyKey: kubernetes.io/hostname
{{- end }}
{{- end }}
{{- end }}
