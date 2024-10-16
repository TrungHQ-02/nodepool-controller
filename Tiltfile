load('ext://restart_process', 'docker_build_with_restart')

compile_cmd = 'CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o build/nodepool-controller ./'

local_resource(
  'nodepool-controller-compile',
  compile_cmd,
  deps=['./main.go', './controller/nodepool-controller.go '],
)
 
docker_build_with_restart(
  'k3d-localregistry.localhost:12345/nodepool-controller-image',
  '.',
  entrypoint=['/app/build/nodepool-controller'],
  dockerfile='local/Dockerfile',
  only=[
    './build',
  ],
  live_update=[
    sync('./build', '/app/build'),
  ],
)

k8s_yaml('local/deployment.yaml')
k8s_resource('nodepool-controller', port_forwards=8080,
            resource_deps=['nodepool-controller-compile'])
