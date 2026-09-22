# Webhook delivery repair workload

`service.py` is intentionally incomplete. Read `TASK.md`, then repair the
service using only Python's standard library.

Run it locally with a disposable database path:

```sh
python3 service.py --port 8080 --db /tmp/webhooks.sqlite
```

The acceptance checker owns its recipients, ports, database paths, and process
lifecycle. The service should not need network access beyond the loopback
recipient URLs supplied through the API.
