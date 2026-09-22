import json
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch
import server

class ValidationTests(unittest.TestCase):
    def test_valid_and_row_numbered_errors(self):
        records, errors = server.validate_csv("id,name,email\n1,Ada,ada@example.com\n2,,bad email\n3,Lin,lin@example.com\n")
        self.assertEqual(records, [{"id": "1", "name": "Ada", "email": "ada@example.com"}, {"id": "3", "name": "Lin", "email": "lin@example.com"}])
        self.assertEqual(errors, [{"row": 3, "message": "empty field: name"}])
    def test_header_and_column_shape_are_errors(self):
        records, errors = server.validate_csv("name,id,email\nA,1,a@b\n2,too,many,columns\n")
        self.assertEqual(records, [])
        self.assertEqual([error["row"] for error in errors], [1])
    def test_store_persists_imports(self):
        with tempfile.TemporaryDirectory() as directory:
            first = server.ImportStore(directory).create("id,name,email\n1,A,a@b\n")
            second = server.ImportStore(directory).get(first["id"])
            self.assertEqual(second["records"][0]["email"], "a@b")
            self.assertEqual(json.loads(Path(directory, "imports.json").read_text())[0]["id"], first["id"])

    def test_failed_save_does_not_claim_an_import_before_retry(self):
        with tempfile.TemporaryDirectory() as directory:
            store = server.ImportStore(directory)
            original_save = store._save
            attempts = [0]
            def fail_once():
                if attempts[0] == 0:
                    attempts[0] += 1
                    raise OSError("disk full")
                return original_save()
            with patch.object(store, "_save", side_effect=fail_once):
                with self.assertRaises(OSError):
                    store.create("id,name,email\n1,A,a@b\n", "retry-me")
                self.assertEqual(store.items, [])
                item, replay = store.create_or_replay("id,name,email\n1,A,a@b\n", "retry-me")
            self.assertFalse(replay)
            self.assertEqual(server.ImportStore(directory).get(item["id"])["records"][0]["id"], "1")

    def test_empty_request_id_replays_and_conflicts(self):
        with tempfile.TemporaryDirectory() as directory:
            store = server.ImportStore(directory)
            source = "id,name,email\n1,A,a@b\n"
            first = store.create(source, "")
            second, replay = store.create_or_replay(source, "")
            self.assertTrue(replay)
            self.assertEqual(second["id"], first["id"])
            with self.assertRaises(server.RequestConflict):
                store.create(source.replace("1,A", "2,B"), "")
            self.assertEqual(len(store.items), 1)

if __name__ == "__main__":
    unittest.main()
