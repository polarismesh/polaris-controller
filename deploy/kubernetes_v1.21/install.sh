#!/bin/bash

set -e

# preprocess the variables

function replaceVar() {
  for file in `ls *.yaml`
  do
    key="#$1#"
    echo "process replace file $file, key $key, value $2"
    sed -i "s/$key/$2/g" $file
  done
}

varFile="../variables.txt"
if [ ! -f "$varFile" ]; then
  echo "../variables.txt not exists"
  exit(1)
fi

export -f replaceVar

cat $varFile | awk -F '=' '{print "replaceVar", $1, $2 | "/bin/bash"}'

kubectl apply -f namespace.yaml
kubectl create secret generic polaris-sidecar-injector -n polaris-system \
--from-file=secrets/key.pem \
--from-file=secrets/cert.pem \
--from-file=secrets/ca-cert.pem

kubectl apply -f rbac.yaml
kubectl apply -f polaris-client-config-tpl.yaml
kubectl apply -f configmap.yaml
kubectl apply -f injector.yaml
kubectl apply -f polaris-metrics-svc.yaml
kubectl apply -f polaris-controller.yaml