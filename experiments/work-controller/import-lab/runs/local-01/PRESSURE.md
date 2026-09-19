# Newly explicit operating needs

Simulated operator feedback, delivered to both builders after baseline evaluation:

People now submit larger files and use the app concurrently. Support 50,000 valid rows within
10 MiB. The health endpoint should remain responsive (2 seconds) and a small concurrent import
should finish within 5 seconds when overlap is observable. Reject bodies above 10 MiB with a
clear 400/413 response. Malformed CSV, including an unterminated quote, must not produce valid
records from malformed input. Preserve completed imports across service restart.

Clients sometimes lose the response and retry. The same request_id and CSV must refer to the
same import without duplicate rows or jobs. Reusing that ID with different CSV must return 409.
Show useful progress/results/errors in the browser. A service restart must not silently lose
accepted pending work; if there is no pending interval we can observe, report that test as
not covered instead of pretending it passed.

Choose the implementation. A queue, database, telemetry subsystem or extra worker is not a
requirement by itself. Report what you changed, why, and which new work is actually necessary.
