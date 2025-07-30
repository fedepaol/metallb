#!/bin/bash

# Create 400 nginx deployments and services
for i in {1..400}; do
    cat <<EOF
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: nginx-$i
spec:
  selector:
    matchLabels:
      app: nginx-$i
  template:
    metadata:
      labels:
        app: nginx-$i
    spec:
      containers:
      - name: nginx
        image: nginx:1
        ports:
        - name: http
          containerPort: 80

---
apiVersion: v1
kind: Service
metadata:
  name: nginx-$i
spec:
  ports:
  - name: http
    port: 80
    protocol: TCP
    targetPort: 80
  selector:
    app: nginx-$i
  type: LoadBalancer

EOF
done > 400-services.yaml

echo "Created 400-services.yaml with 400 nginx deployments and services"
echo "To apply: kubectl apply -f 400-services.yaml" 