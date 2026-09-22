import json
import threading
import unittest
from http.client import HTTPConnection
from http.server import ThreadingHTTPServer

from server import Handler, ImportStore, validate_csv


class ValidationTests(unittest.TestCase):
    def test_keeps_valid_records_and_reports_numbered_errors(self):
        records, errors = validate_csv("id,name,email\n1,Ada,ada@example.com\n2,,bad email\n3,Grace,grace@example.com\n")
        self.assertEqual(records, [{"id": "1", "name": "Ada", "email": "ada@example.com"}, {"id": "3", "name": "Grace", "email": "grace@example.com"}])
        self.assertEqual(errors, [{"row": 3, "message": "missing name"}])

    def test_rejects_missing_columns_and_bad_email(self):
        _, errors = validate_csv("id,name\n1,Ada\n")
        self.assertEqual(errors[0]["row"], 1)
        self.assertIn("email", errors[0]["message"])
        _, errors = validate_csv("id,name,email\n1,Ada,a@@example.com\n")
        self.assertEqual(errors[0]["row"], 2)

    def test_rejects_raw_email_spaces(self):
        for email in (" ada@example.com", "ada@example.com ", "ada @example.com"):
            records, errors = validate_csv("id,name,email\n1,Ada," + email + "\n")
            self.assertEqual(records, [])
            self.assertEqual(errors, [{"row": 2, "message": "email must not contain spaces"}])


class EndpointTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        Handler.store = ImportStore()
        cls.server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        cls.thread = threading.Thread(target=cls.server.serve_forever, daemon=True)
        cls.thread.start()
        cls.host, cls.port = cls.server.server_address

    @classmethod
    def tearDownClass(cls):
        cls.server.shutdown()
        cls.thread.join()

    def request(self, method, path, body=None):
        connection = HTTPConnection(self.host, self.port)
        encoded = None if body is None else json.dumps(body).encode()
        headers = {} if encoded is None else {"Content-Type": "application/json"}
        connection.request(method, path, encoded, headers)
        response = connection.getresponse()
        content_type = response.getheader("Content-Type", "")
        payload = json.loads(response.read()) if content_type.startswith("application/json") else response.read().decode()
        connection.close()
        return response.status, payload

    def test_health_create_list_and_detail(self):
        status, payload = self.request("GET", "/health")
        self.assertEqual((status, payload), (200, {"ok": True}))
        status, created = self.request("POST", "/imports", {"csv": "id,name,email\n1,Ada,ada@example.com\n2,Bad,bad email\n"})
        self.assertEqual(status, 201)
        self.assertEqual(created["status"], "failed")
        status, detail = self.request("GET", "/imports/" + created["id"])
        self.assertEqual(status, 200)
        self.assertEqual(len(detail["records"]), 1)
        self.assertEqual(detail["errors"][0]["row"], 3)
        status, listing = self.request("GET", "/imports")
        self.assertEqual(status, 200)
        self.assertEqual(listing["imports"][0]["id"], created["id"])


if __name__ == "__main__":
    unittest.main()
