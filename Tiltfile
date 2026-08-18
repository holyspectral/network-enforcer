tilt_settings_file = "./tilt-settings.yaml"
settings = read_yaml(tilt_settings_file)

allow_k8s_contexts(settings.get("clusters"))

update_settings(
    k8s_upsert_timeout_secs=180,
)

# Create the namespace
# This is required since the helm() function doesn't support the create_namespace flag
release_namespace = "network-enforcer"
load("ext://namespace", "namespace_create")
namespace_create(release_namespace)

controller_image = settings.get("controller").get("image")

# Endpoint of the local dev OTel collector deployed below. The controller reaches
# it insecurely (plaintext gRPC) via the chart's `external` telemetry strategy.
dev_otel_collector_endpoint = (
    "dev-otel-collector." + release_namespace + ".svc.cluster.local:4317"
)

# Prepare Helm set values based on CNI type
helm_set_values = [
    "controller.image.repository=" + controller_image,
    "controller.replicas=1",
    "controller.containerSecurityContext.runAsUser=null",
    "controller.podSecurityContext.runAsNonRoot=false",
    # Point the controller at the local dev collector (see hack/dev-otel-collector.yaml).
    # `external` with no cert secret leaves OTEL_EXPORTER_OTLP_CERTIFICATE unset, so
    # the controller exports OTLP over plaintext gRPC (insecure when CA cert is empty)
    # and no cert-manager is needed for local development.
    "telemetry.collectorStrategy=external",
    "telemetry.externalCollector.endpoint=" + dev_otel_collector_endpoint,
    "telemetry.externalCollector.protocol=grpc",
]

yaml = helm(
    "./charts/network-enforcer",
    name="network-enforcer",
    namespace=release_namespace,
    set=helm_set_values
)

k8s_yaml(yaml)

# Local dev OpenTelemetry collector. It prints every violation event the
# controller emits (policy_violation_observed / policy_deny) to its logs via the
# debug exporter, so they are visible in the Tilt UI. See hack/dev-otel-collector.yaml.
k8s_yaml("./hack/dev-otel-collector.yaml")
k8s_resource(
    "dev-otel-collector",
    labels=["telemetry"],
    # Forward the OTLP gRPC port so violations can also be sent from the host.
    port_forwards=["4317:4317"],
)

# Hot reloading containers
local_resource(
    "controller_tilt",
    "make controller",
    deps=[
        "go.mod",
        "go.sum",
        "cmd/controller",
        "api",
        "internal",
    ],
)

entrypoint = ["/controller"]
dockerfile = "./hack/Dockerfile.controller.tilt"

load("ext://restart_process", "docker_build_with_restart")
docker_build_with_restart(
    controller_image,
    ".",
    dockerfile=dockerfile,
    entrypoint=entrypoint,
    # `only` here is important, otherwise, the container will get updated
    # on _any_ file change.
    only=[
        "./bin/controller",
    ],
    live_update=[
        sync("./bin/controller", "/controller"),
    ],
)
