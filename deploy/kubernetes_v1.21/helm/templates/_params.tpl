{{/* vim: set filetype=mustache: */}}
{{/* Note: In the controller, the go template will be used again to render the configuration,
so some symbols {{ expr }} need to be kept from being rendered here. */}}

{{/*
Define the cmd args for the bootstrap init container.
*/}}
{{- define "configmap-client.config_tpl" -}}
apiVersion: v1
kind: ConfigMap
metadata:
  namespace: {{ "{{" }} .Namespace {{ "}}" }}
  name: {{ "{{" }} .Name {{ "}}" }}
data:
  polaris.yaml: |-
    global:
      serverConnector:
        addresses:
          - {{ "{{" }} .PolarisServer {{ "}}" }}
{{- end -}}


{{/*
Define the volume mounts for the sidecar container.
*/}}
{{- define "configmap-sidecar.polaris_volume_mounts" -}}
- name: sds
  mountPath: /var/run/polaris/mtls
{{ "{{" }} if ne ( index .ObjectMeta.Annotations `polarismesh.cn/tls-mode`) "none" {{ "}}" }}
- name: root-ca
  mountPath: /etc/polaris-sidecar/certs
{{ "{{" }} end {{ "}}" }}
{{- end -}}

{{/*
Define the volume for the bootstrap init container.
*/}}
{{- define "configmap-sidecar.bootstrap_volume" -}}
- name: sds
  emptyDir: {}
{{ "{{" }} if ne ( index .ObjectMeta.Annotations `polarismesh.cn/tls-mode`) "none"{{ "}}" }}
- name: root-ca
  secret:
    secretName: polaris-sidecar-secret
    items:
    - key: root-cert
      path: rootca.pem
{{ "{{" }} end {{ "}}" }}
- name: polaris-client-config
  emptyDir: {}
- name: envoy-bootstrap
  emptyDir: {}
- name: envoy-logs
  emptyDir: {}
- name: polaris-dir
  emptyDir: {}
- name: polaris-log
  emptyDir: {}
{{- end -}}


{{/*
Define the cmd envs for the bootstrap init container.
*/}}
{{- define "configmap-sidecar.bootstrap_envs" -}}
- name: METADATA
  value: "{{ "{{" }} index .ObjectMeta.Annotations `sidecar.polarismesh.cn/envoyMetadata` {{ "}}" }}"
{{ "{{" }}if ne ( index .ObjectMeta.Annotations `polarismesh.cn/tls-mode`) "none"{{ "}}" }}
- name: TLS_MODE
  value: "{{ "{{" }}index .ObjectMeta.Annotations `polarismesh.cn/tls-mode`{{ "}}" }}"
{{ "{{" }}end{{ "}}" }}
- name: NAMESPACE
  valueFrom:
    fieldRef:
      fieldPath: metadata.namespace
- name: POD_NAME
  valueFrom:
    fieldRef:
      fieldPath: metadata.name
- name: INSTANCE_IP
  valueFrom:
    fieldRef:
      fieldPath: status.podIP
- name: POLARIS_SERVER_URL
  value: {{ "{{" }}.ProxyConfig.ProxyMetadata.serverAddress{{ "}}" }}:15010
- name: POLARIS_SERVER_HOST
  value: {{ "{{" }}.ProxyConfig.ProxyMetadata.serverAddress{{ "}}" }}
- name: POLARIS_SERVER_PORT
  value: 15010
- name: CLUSTER_NAME
  value: {{ "{{" }}.ProxyConfig.ProxyMetadata.clusterName{{ "}}" }}
- name: OPEN_DEMAND
  value: {{ "{{" }}.ProxyConfig.ProxyMetadata.opemDemand{{ "}}" }}
{{- end -}}


{{/*
Define the container resources for the envoy container.
*/}}
{{- define "configmap-sidecar.envoy_resources" -}}
{{ "{{" }}- if or (isset .ObjectMeta.Annotations `polarismesh.cn/proxyCPU`) (isset .ObjectMeta.Annotations `polarismesh.cn/proxyMemory`) (isset .ObjectMeta.Annotations `polarismesh.cn/proxyCPULimit`) (isset .ObjectMeta.Annotations `polarismesh.cn/proxyMemoryLimit`) {{ "}}" }}
  {{ "{{" }}- if or (isset .ObjectMeta.Annotations `polarismesh.cn/proxyCPU`) (isset .ObjectMeta.Annotations `polarismesh.cn/proxyMemory`) {{ "}}" }}
    requests:
      {{ "{{" }} if (isset .ObjectMeta.Annotations `polarismesh.cn/proxyCPU`) -{{ "}}" }}
      cpu: "{{ "{{" }} index .ObjectMeta.Annotations `polarismesh.cn/proxyCPU` {{ "}}" }}"
      {{ "{{" }} end {{ "}}" }}
      {{ "{{" }} if (isset .ObjectMeta.Annotations `polarismesh.cn/proxyMemory`) -{{ "}}" }}
      memory: "{{ "{{" }} index .ObjectMeta.Annotations `polarismesh.cn/proxyMemory` {{ "}}" }}"
      {{ "{{" }} end {{ "}}" }}
  {{ "{{" }}- end {{ "}}" }}
  {{ "{{" }}- if or (isset .ObjectMeta.Annotations `polarismesh.cn/proxyCPULimit`) (isset .ObjectMeta.Annotations `polarismesh.cn/proxyMemoryLimit`) {{ "}}" }}
    limits:
      {{ "{{" }} if (isset .ObjectMeta.Annotations `polarismesh.cn/proxyCPULimit`) -{{ "}}" }}
      cpu: "{{ "{{" }} index .ObjectMeta.Annotations `polarismesh.cn/proxyCPULimit` {{ "}}" }}"
      {{ "{{" }} end {{ "}}" }}
      {{ "{{" }} if (isset .ObjectMeta.Annotations `polarismesh.cn/proxyMemoryLimit`) -{{ "}}" }}
      memory: "{{ "{{" }} index .ObjectMeta.Annotations `polarismesh.cn/proxyMemoryLimit` {{ "}}" }}"
      {{ "{{" }} end {{ "}}" }}
  {{ "{{" }}- end {{ "}}" }}
{{ "{{" }}- else {{ "}}" }}
  {{ "{{" }}- if .Values.global.proxy.resources {{ "}}" }}
    {{ "{{" }} toYaml .Values.global.proxy.resources | indent 6 {{ "}}" }}
  {{ "{{" }}- end {{ "}}" }}
{{ "{{" }}- end {{ "}}" }}
{{- end -}}