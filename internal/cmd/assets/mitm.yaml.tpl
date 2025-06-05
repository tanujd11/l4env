apiVersion: v1
data:
  config: {{ .MITMSecretConfig }}
kind: Secret
metadata:
  creationTimestamp: null
  name: mitm-config
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: configmap-reader-binding
subjects:
- kind: ServiceAccount
  name: mitm-sa
roleRef:
  kind: Role
  name: configmap-reader
  apiGroup: rbac.authorization.k8s.io
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: configmap-reader
rules:
- apiGroups: [""]
  resources: ["configmaps"]
  verbs: ["get", "list", "watch"]
- apiGroups: [""]
  resources: ["secrets"]
  verbs: ["get", "list", "watch"]
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: mitm-sa
---
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: envoy-tproxy
  labels:
    app: envoy-tproxy
spec:
  selector:
    matchLabels:
      app: envoy-tproxy
  template:
    metadata:
      labels:
        app: envoy-tproxy
    spec:
      serviceAccountName: mitm-sa
      hostNetwork: true
      dnsPolicy: ClusterFirstWithHostNet
{{ if .ImagePullSecretData }}
      imagePullSecrets:
        - name: ecr-creds
{{ end }}
      initContainers:
      - name: ext-proc
        restartPolicy: Always
        securityContext:
          runAsUser: 101
          runAsGroup: 101
        ports:
          - containerPort: 8080 # metrics
        env:
        - name: DEBUG
          value: "true"
        - name: SENSITIVE_LOGGING
          value: "true"
        - name: MEM_REQUEST
          valueFrom:
            resourceFieldRef:
              containerName: ext-proc
              resource: requests.memory
        - name: CPU_REQUEST
          valueFrom:
            resourceFieldRef:
              containerName: ext-proc
              resource: requests.cpu
        - name: GOMAXPROCS
          value: "2"
        - name: NAMESPACE
          valueFrom:
            fieldRef:
              apiVersion: v1
              fieldPath: metadata.namespace
        - name: APP_LABEL
          valueFrom:
            fieldRef:
              apiVersion: v1
              fieldPath: metadata.labels['app']
        image: {{ .ExtProcImage }}
        imagePullPolicy: Always
        startupProbe:
          exec:
            command:
              - /workspace/healthcheck
        livenessProbe:
          exec:
            command:
              - /workspace/healthcheck
        readinessProbe:
          exec:
            command:
              - /workspace/healthcheck
        volumeMounts:
        - name: socket-dir
          mountPath: /var/sock/composer
        - name: build-dir
          mountPath: /usr/local/share/composer
        - name: ext-proc-internal
          mountPath: /usr/local/share/result/
        - name: envoy-config
          mountPath: /etc/envoy/config
        - name: ca-store
          mountPath: /etc/envoy/certs
      containers:
      - name: envoy
        image: {{ .EnvoyImage }}
        imagePullPolicy: IfNotPresent
        ports:
          - containerPort: 8443
            name: https
        volumeMounts:
          - name: socket-dir
            mountPath: /var/sock/composer
          - name: ca-store
            mountPath: /etc/envoy/certs
            readOnly: true
          - name: envoy-config
            mountPath: /etc/envoy/config
        command: [ "envoy" ]
        args:
          [
            "-c", "/etc/envoy/config/envoy.yaml",
            "--log-level", "debug", "--base-id", "1",
          ]
        securityContext:
          runAsUser: 101
          runAsGroup: 101
          capabilities:
            add: ["NET_ADMIN"]
      volumes:
        - name: envoy-config
          emptyDir: {}

        - name: build-dir
          emptyDir: {}

        - name: ca-store
          emptyDir: {}

        - name: socket-dir
          emptyDir: {}

        - name: ext-proc-internal
          emptyDir: {}
---
{{ if .ImagePullSecretData }}
apiVersion: v1
data:
  .dockerconfigjson: {{ .ImagePullSecretData }}
kind: Secret
metadata:
  creationTimestamp: null
  name: ecr-creds
type: kubernetes.io/dockerconfigjson
{{ end }}
---
apiVersion: v1
kind: Service
metadata:
  name: envoy-tproxy
  labels:
    app: envoy-tproxy
spec:
  type: LoadBalancer
  loadBalancerIP: {{ .MITMVIP }}
  selector:
    app: envoy-tproxy
  ports:
    - name: tproxy
      port: 15001
      targetPort: 15001
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: composer-config
  labels:
    app: envoy-tproxy
    extension.tetrate.io/composer-config: "true"
data:
  config: |-
    plugins:
      - name: "pluggable"
        config:
          secretName: mitm-config
      - name: "aidiscovery"
        config:
          secretName: mitm-config
      - name: "payload-detector"
        configMapName: "payload-detector"
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: payload-detector
  namespace: default
data:
  server.go: |-
    package main

    import (
      "context"
      "fmt"
      "regexp"
      "strings"

      "github.com/tetrateio/extensibility/ext-proc/golang/api/extproc"
      "github.com/tetrateio/extensibility/ext-proc/golang/mitm-discovery/reporter"
      "github.com/tetrateio/extensibility/ext-proc/golang/mitm-discovery/reporter/api"
      "go.uber.org/zap"
    )

    var Priority int32 = 999

    func NewExtProcPluginFactory(unparsedConfig []byte, files map[string][]byte) (extproc.ExtProcPluginFactory, error) {
      return &f{}, nil
    }

    type f struct{}

    func (f *f) NewRawPlugin(_ []byte, _ map[string][]byte, _ extproc.Handle) (extproc.ExtProcRawPlugin, error) {
      return nil, nil
    }

    func (f *f) New(c []byte, _ map[string][]byte, handle extproc.Handle) (extproc.ExtProcPlugin, error) {
      return &s{
        logger:         handle.Logger(),
        requestHeaders: map[string][]string{},
      }, nil
    }

    type s struct {
      // embeds DefaultExtProcPlugin, which implements all necessary methods for ExtProcPlugin interface.
      // You just have to implement what you want to implement only.
      // In this example, we just implement ResponseHeaders method only.
      extproc.DefaultExtProcPlugin

      logger         *zap.Logger
      requestHeaders map[string][]string
    }

    type Header struct {
      Key   string `yaml:"key"`
      Value string `yaml:"value"`
    }

    func (s *s) RequestHeaders(ctx context.Context, headers map[string][]string) (mutatedHeaders map[string][]string, err error) {
      // Need to save headers for later use in RequestBody
      // (Request header is necessary to detect AI model)
      s.requestHeaders = headers
      return headers, nil
    }

    func (s *s) RequestBody(ctx context.Context, body string, _ bool) (mutatedBody string, err error) {
      s.logger.Debug("RequestBody", zap.Any("body", body))
      m, err := reporter.ModelParser.Parse(&api.RawRequestData{
        RequestHeaders: s.requestHeaders,
        RequestBody:    body,
      })
      if err != nil {
        s.logger.Error("failed to parse model", zap.Error(err))
      }
      /*
          if strings.Contains(body, "@tetrate.io") {
          reporter.NotificationReporter.ReportNotification(s.requestHeaders, "WARN", fmt.Sprintf("an email (@tetrate.io) is detected in the request body"))
          reporter.MetricsReporter.ReportMetric(s.requestHeaders, "detected_email", "1")
          }
          if strings.Contains(body, "password:") {
          reporter.NotificationReporter.ReportNotification(s.requestHeaders, "WARN", fmt.Sprintf("an password is detected in the request body"))
          reporter.MetricsReporter.ReportMetric(s.requestHeaders, "detected_password", "1")
          }
      */

      if strings.Contains(body, "credentials:") {
        reporter.NotificationReporter.ReportNotification(s.requestHeaders, "WARN", fmt.Sprintf("credentials is detected in the request body"))
        reporter.MetricsReporter.ReportMetric(s.requestHeaders, "detected_credentials", "1")
      }

      //Email
      matched, err := regexp.MatchString(`(?i)[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`, body)
      if err != nil {
        reporter.NotificationReporter.ReportNotification(s.requestHeaders, "WARN", fmt.Sprintf("Unable to parse Regular Expression for email detection"))
      }
      if matched {
        reporter.MetricsReporter.ReportMetric(s.requestHeaders, "email_detected", "1")
        reporter.NotificationReporter.ReportNotification(s.requestHeaders, "WARN", fmt.Sprintf("Detected Email Usage in Prompt : %s", m.Prompt))
      }

      //Phone
      matched, err = regexp.MatchString(`^\+?[1-9]\d{1,14}$`, body)
      if err != nil {
        reporter.NotificationReporter.ReportNotification(s.requestHeaders, "WARN", fmt.Sprintf("Unable to parse Regular Expression for phone no usage detection"))
      }
      if matched {
        reporter.MetricsReporter.ReportMetric(s.requestHeaders, "phone_detected", "1")
        reporter.NotificationReporter.ReportNotification(s.requestHeaders, "WARN", fmt.Sprintf("Detected Phone Number Usage in Prompt : %s", m.Prompt))
      }

      //Phone
      matched, err = regexp.MatchString(`(?i)(?:\+?\d{1,3}[-.\s]?)?(?:\(\d{1,3}\)[-.\s]?)?\d{3,4}[-.\s]?\d{4}`, body)
      if err != nil {
        reporter.NotificationReporter.ReportNotification(s.requestHeaders, "WARN", fmt.Sprintf("Unable to parse Regular Expression for phone no usage detection"))
      }
      if matched {
        reporter.MetricsReporter.ReportMetric(s.requestHeaders, "phone_detected", "1")
        reporter.NotificationReporter.ReportNotification(s.requestHeaders, "WARN", fmt.Sprintf("Detected Phone Number Usage in Prompt : %s", m.Prompt))
      }

      //US SSN
      SSNpattern :=
        `(?:^|[^0-9])` + // start of string or non‑digit
          `\d{3}[-‑]\d{2}[-‑]\d{4}` + // SSN with either hyphen type
          `(?:[^0-9]|$)` // non‑digit or end of string
      matched, err = regexp.MatchString(SSNpattern, body)
      if err != nil {
        reporter.NotificationReporter.ReportNotification(s.requestHeaders, "WARN", fmt.Sprintf("Unable to parse Regular Expression for SSN usage detection"))
      }
      if matched {
        reporter.MetricsReporter.ReportMetric(s.requestHeaders, "ssn_detected", "1")
        reporter.NotificationReporter.ReportNotification(s.requestHeaders, "WARN", fmt.Sprintf("Detected Social Security Number Usage in Prompt : %s", m.Prompt))
      }

      //Credit Card
      ccPattern := `\b(?:` +
        `4[0-9]{3}(?:[-\s]?[0-9]{4}){3}|` + // Visa
        `5[1-5][0-9]{2}(?:[-\s]?[0-9]{4}){3}|` + // Mastercard
        `3[47][0-9]{2}(?:[-\s]?[0-9]{4}){2}[-\s]?[0-9]{3}|` + // AmEx
        `6(?:011|5[0-9]{2})(?:[-\s]?[0-9]{4}){3}` + // Discover
        `)\b`
      matched, err = regexp.MatchString(ccPattern, body)
      if err != nil {
        reporter.NotificationReporter.ReportNotification(s.requestHeaders, "WARN", fmt.Sprintf("Unable to parse Regular Expression for Credit Card usage detection"))
      }
      if matched {
        reporter.MetricsReporter.ReportMetric(s.requestHeaders, "credit_card_detected", "1")
        reporter.NotificationReporter.ReportNotification(s.requestHeaders, "WARN", fmt.Sprintf("Detected Credit Card Number Usage in Prompt : %s", m.Prompt))
      }

      //ZIP Code
      matched, err = regexp.MatchString(`\b\d{5}(?:-\d{4})?\b`, body)
      if err != nil {
        reporter.NotificationReporter.ReportNotification(s.requestHeaders, "WARN", fmt.Sprintf("Unable to parse Regular Expression for ZipCode usage detection"))
      }
      if matched {
        reporter.MetricsReporter.ReportMetric(s.requestHeaders, "zip_code_detected", "1")
        reporter.NotificationReporter.ReportNotification(s.requestHeaders, "WARN", fmt.Sprintf("Detected ZipCode Number Usage in Prompt : %s", m.Prompt))
      }

      //ISO Date
      matched, err = regexp.MatchString(`\b\d{4}-(0[1-9]|1[0-2])-(0[1-9]|[12]\d|3[01])\b`, body)
      if err != nil {
        reporter.NotificationReporter.ReportNotification(s.requestHeaders, "WARN", fmt.Sprintf("Unable to parse Regular Expression for ISO Date usage detection"))
      }
      if matched {
        reporter.MetricsReporter.ReportMetric(s.requestHeaders, "ISO_date_detected", "1")
        reporter.NotificationReporter.ReportNotification(s.requestHeaders, "WARN", fmt.Sprintf("Detected ISO Date Number Usage in Prompt : %s", m.Prompt))
      }

      //IP v4
      matched, err = regexp.MatchString(`\b((25[0-5]|2[0-4]\d|[01]?\d?\d)\.){3}(25[0-5]|2[0-4]\d|[01]?\d?\d)\b`, body)
      if err != nil {
        reporter.NotificationReporter.ReportNotification(s.requestHeaders, "WARN", fmt.Sprintf("Unable to parse Regular Expression for IPV4 usage detection"))
      }
      if matched {
        reporter.MetricsReporter.ReportMetric(s.requestHeaders, "IPV4_detected", "1")
        reporter.NotificationReporter.ReportNotification(s.requestHeaders, "WARN", fmt.Sprintf("Detected IPv4 Usage in Prompt : %s", m.Prompt))
      }

      //IP v6
      IPV6pattern := `\b(?:` +
        `(?:[0-9A-Fa-f]{1,4}:){7}[0-9A-Fa-f]{1,4}|` + // full
        `(?:[0-9A-Fa-f]{1,4}:){1,7}:|` + // x::
        `(?:[0-9A-Fa-f]{1,4}:){1,6}:[0-9A-Fa-f]{1,4}|` + // x:x::x
        `(?:[0-9A-Fa-f]{1,4}:){1,5}(?::[0-9A-Fa-f]{1,4}){1,2}|` +
        `(?:[0-9A-Fa-f]{1,4}:){1,4}(?::[0-9A-Fa-f]{1,4}){1,3}|` +
        `(?:[0-9A-Fa-f]{1,4}:){1,3}(?::[0-9A-Fa-f]{1,4}){1,4}|` +
        `(?:[0-9A-Fa-f]{1,4}:){1,2}(?::[0-9A-Fa-f]{1,4}){1,5}|` +
        `(?:[0-9A-Fa-f]{1,4}:){1}(?::[0-9A-Fa-f]{1,4}){1,6}|` +
        `:(?:(?::[0-9A-Fa-f]{1,4}){1,7}|:)` +
        `)\b`

      matched, err = regexp.MatchString(IPV6pattern, body)
      if err != nil {
        reporter.NotificationReporter.ReportNotification(s.requestHeaders, "WARN", fmt.Sprintf("Unable to parse Regular Expression for IPv6 usage detection"))
      }
      if matched {
        reporter.MetricsReporter.ReportMetric(s.requestHeaders, "IPV6_detected", "1")
        reporter.NotificationReporter.ReportNotification(s.requestHeaders, "WARN", fmt.Sprintf("Detected IPv6 Usage in Prompt : %s", m.Prompt))
      }

      //IBAN
      IBANpattern :=
        `(?:^|[^A-Za-z0-9])` + // start or non‑alnum
          `[A-Z]{2}[0-9]{2}` + // country + check digits
          `(?:\s?[A-Z0-9]{4}){3,7}` + // 3–7 groups of 4 alnum, optional space
          `(?:$|[^A-Za-z0-9])` // end or non‑alnum
      matched, err = regexp.MatchString(IBANpattern, body)
      if err != nil {
        reporter.NotificationReporter.ReportNotification(s.requestHeaders, "WARN", fmt.Sprintf("Unable to parse Regular Expression for IBAN usage detection"))
      }
      if matched {
        reporter.MetricsReporter.ReportMetric(s.requestHeaders, "IBAN_detected", "1")
        reporter.NotificationReporter.ReportNotification(s.requestHeaders, "WARN", fmt.Sprintf("Detected IBAN Usage in Prompt : %s", m.Prompt))
      }

      reporter.MetricsReporter.ReportMetric(s.requestHeaders, "prompts_scanned", "1")

      return body, nil
    }
