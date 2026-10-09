INSERT INTO ingresses (manifest)
VALUES ('{
  "apiVersion":"networking.k8s.io/v1",
  "kind":"Ingress",
  "metadata":{"name":"web","namespace":"sql-medium-insert-ingress"},
  "spec":{
    "ingressClassName":"nginx",
    "defaultBackend":{"service":{"name":"web","port":{"number":80}}}
  }
}');
