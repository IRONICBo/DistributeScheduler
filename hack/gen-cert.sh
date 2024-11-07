#!/usr/bin/env bash

BASEDIR=/tmp/webhook/certs/
SERVICE_NAME=webhook-server
NAMESPACE=cloudpilot
set -x # Print commands

rm -rf $BASEDIR
mkdir -p $BASEDIR

cat > $BASEDIR/server.conf <<EOF
[req]
req_extensions = v3_req
distinguished_name = req_distinguished_name
prompt = no
[req_distinguished_name]
CN = ${SERVICE_NAME}.${NAMESPACE}.svc
[ v3_req ]
basicConstraints = CA:FALSE
keyUsage = nonRepudiation, digitalSignature, keyEncipherment
extendedKeyUsage = clientAuth, serverAuth
subjectAltName = @alt_names
[alt_names]
DNS.1 = ${SERVICE_NAME}
DNS.2 = ${SERVICE_NAME}.${NAMESPACE}
DNS.3 = ${SERVICE_NAME}.${NAMESPACE}.svc
EOF

openssl req -x509 -nodes -days 365 -newkey rsa:2048 -keyout $BASEDIR/ca.key -out $BASEDIR/ca.crt -subj "/CN=${SERVICE_NAME}.${NAMESPACE}.svc"

openssl genrsa -out $BASEDIR/${SERVICE_NAME}-tls.key 2048
openssl req -new -key $BASEDIR/${SERVICE_NAME}-tls.key -subj "/CN=${SERVICE_NAME}.${NAMESPACE}.svc" -config $BASEDIR/server.conf \
    | openssl x509 -req -CA $BASEDIR/ca.crt -CAkey $BASEDIR/ca.key -CAcreateserial -out $BASEDIR/${SERVICE_NAME}-tls.crt -extensions v3_req -extfile $BASEDIR/server.conf

cp $BASEDIR/${SERVICE_NAME}-tls.crt $BASEDIR/tls.crt
cp $BASEDIR/${SERVICE_NAME}-tls.key $BASEDIR/tls.key

echo "Creating K8s secret..."
kubectl create namespace $NAMESPACE || true
kubectl -n $NAMESPACE delete secret ${SERVICE_NAME}-tls || true
kubectl -n $NAMESPACE create secret tls ${SERVICE_NAME}-tls \
    --cert=$BASEDIR/${SERVICE_NAME}-tls.crt \
    --key=$BASEDIR/${SERVICE_NAME}-tls.key

ca_pem_b64="$(cat $BASEDIR/ca.crt | base64 | tr -d '\n')"
echo "CA_PEM_B64: $ca_pem_b64"

echo "caBundle: ${ca_pem_b64}"

set +x # Stop printing commands
