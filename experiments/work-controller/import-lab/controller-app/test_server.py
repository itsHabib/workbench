import json
import tempfile
import unittest
from pathlib import Path
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

if __name__ == "__main__":
    unittest.main()
