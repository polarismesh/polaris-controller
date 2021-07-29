#!/bin/bash

cat ./polaris-controller-tmp.yaml | ./polaris-controller-patch-cluster.sh > ./polaris-controller.yaml

kubectl apply -f namespace.yaml
kubectl create secret generic polaris-sidecar-injector -n polaris-system \
--from-file=secrets/key.pem \
--from-file=secrets/cert.pem \
--from-file=secrets/ca-cert.pem

kubectl apply -f rbac.yaml
kubectl apply -f configmap.yaml
kubectl apply -f injector.yaml
kubectl apply -f polaris-metrics-svc.yaml
kubectl apply -f polaris-controller.yaml