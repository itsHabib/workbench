"""Tokenizer. Emits NEWLINE tokens so statements can end at line ends; the parser
skips them where a line break cannot end anything (inside brackets, after commas)."""

from __future__ import annotations

from dataclasses import dataclass

__all__ = ["Token", "LexError", "tokenize", "KEYWORDS"]

KEYWORDS = {
    "let", "if", "else", "for", "in", "fn", "true", "false", "unit", "and", "or", "not",
    "return", "world", "observe", "act", "cap", "goal", "script", "tactic", "use", "ask", "propose",
}

_PUNCT = ["==", "!=", "<=", ">=", "(", ")", "{", "}", "[", "]", ",", ":", ".", "=", "<", ">", "+", "-", "*", "/", "%"]


@dataclass(frozen=True)
class Token:
    kind: str  # "ident" | "kw" | "int" | "str" | "punct" | "newline" | "eof"
    text: str
    line: int


class LexError(Exception):
    pass


def tokenize(src: str) -> list[Token]:
    toks: list[Token] = []
    i, line, n = 0, 1, len(src)
    while i < n:
        c = src[i]
        if c == "\n":
            toks.append(Token("newline", "\n", line))
            line += 1
            i += 1
            continue
        if c in " \t\r":
            i += 1
            continue
        if c == "#":
            while i < n and src[i] != "\n":
                i += 1
            continue
        if c.isdigit():
            j = i
            while j < n and src[j].isdigit():
                j += 1
            toks.append(Token("int", src[i:j], line))
            i = j
            continue
        if c.isalpha() or c == "_":
            j = i
            while j < n and (src[j].isalnum() or src[j] == "_"):
                j += 1
            word = src[i:j]
            toks.append(Token("kw" if word in KEYWORDS else "ident", word, line))
            i = j
            continue
        if c == '"':
            j, buf = i + 1, []
            while j < n and src[j] != '"':
                if src[j] == "\\" and j + 1 < n:
                    esc = src[j + 1]
                    buf.append({"n": "\n", "t": "\t", '"': '"', "\\": "\\"}.get(esc, esc))
                    j += 2
                    continue
                if src[j] == "\n":
                    raise LexError(f"line {line}: unterminated string")
                buf.append(src[j])
                j += 1
            if j >= n:
                raise LexError(f"line {line}: unterminated string")
            toks.append(Token("str", "".join(buf), line))
            i = j + 1
            continue
        for p in _PUNCT:
            if src.startswith(p, i):
                toks.append(Token("punct", p, line))
                i += len(p)
                break
        else:
            raise LexError(f"line {line}: unexpected character {c!r}")
    toks.append(Token("eof", "", line))
    return toks
