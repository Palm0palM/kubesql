INSERT INTO deployments (manifest)
VALUES ('{
  "apiVersion":"apps/v1",
  "kind":"Deployment",
  "metadata":{"name":"api","namespace":"sql-medium-insert"},
  "spec":{
    "replicas":2,
    "selector":{"matchLabels":{"app":"api"}},
    "template":{
      "metadata":{"labels":{"app":"api"}},
      "spec":{"containers":[{"name":"api","image":"nginx:1.27"}]}
    }
  }
}');
