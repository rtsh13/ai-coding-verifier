// Baked dependency manifest for the Go sandbox image.
//
// This file is GENERATED — do not edit by hand.
// Source: testdata/mining/2026-06-15/go-raw.json
// Mined:  2026-06-15T14:22:14.705470+00:00
// Methodology: repo-prevalence ≥ 10% (see ADR-001)
// K = 42 packages, generated 2026-06-16T14:40:20.934999+00:00

module sandbox/baked

go 1.26

require (
	github.com/aws/aws-sdk-go-v2         v1.41.5
	github.com/aws/aws-sdk-go-v2/config  v1.32.17
	github.com/fatih/color               v1.19.0
	github.com/fsnotify/fsnotify         v1.10.1
	github.com/go-sql-driver/mysql       v1.10.0
	github.com/google/go-cmp             v0.7.0
	github.com/google/uuid               v1.6.0
	github.com/gorilla/mux               v1.8.1
	github.com/gorilla/websocket         v1.5.3
	github.com/klauspost/compress        v1.18.6
	github.com/mattn/go-isatty           v0.0.22
	github.com/pkg/errors                v0.9.1
	github.com/prometheus/client_golang  v1.23.2
	github.com/sirupsen/logrus           v1.9.4
	github.com/spf13/cobra               v1.10.2
	github.com/spf13/pflag               v1.0.10
	github.com/spf13/viper               v1.21.0
	github.com/stretchr/testify          v1.11.1
	go.opentelemetry.io/otel             v1.44.0
	go.opentelemetry.io/otel/sdk         v1.44.0
	go.opentelemetry.io/otel/trace       v1.44.0
	go.uber.org/zap                      v1.28.0
	golang.org/x/crypto                  v0.52.0
	golang.org/x/exp                     v0.0.0-20260410095643-746e56fc9e2f
	golang.org/x/mod                     v0.36.0
	golang.org/x/net                     v0.55.0
	golang.org/x/oauth2                  v0.36.0
	golang.org/x/sync                    v0.20.0
	golang.org/x/sys                     v0.45.0
	golang.org/x/term                    v0.43.0
	golang.org/x/text                    v0.37.0
	golang.org/x/time                    v0.15.0
	golang.org/x/tools                   v0.45.0
	google.golang.org/api                v0.283.0
	google.golang.org/grpc               v1.81.1
	google.golang.org/protobuf           v1.36.11
	gopkg.in/yaml.v2                     v2.4.0
	gopkg.in/yaml.v3                     v3.0.1
	k8s.io/api                           v0.36.1
	k8s.io/apimachinery                  v0.36.1
	k8s.io/client-go                     v0.36.1
	sigs.k8s.io/yaml                     v1.6.0
)
