SELECT name FROM deployments
WHERE name = 'web' OR name = 'worker' AND replicas >= 3;
