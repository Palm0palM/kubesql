SELECT name FROM ingresses WHERE NOT (default_backend_service = NULL);
