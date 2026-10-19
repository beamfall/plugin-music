#!/usr/bin/env python3
"""Small zero-dependency structural YAML loader for the workflow contract.

It intentionally supports only the YAML shapes used by GitHub Actions:
indent-based mappings/sequences, quoted and plain scalars, block scalars, and
flow mappings/sequences. Unknown syntax and duplicate keys fail closed.
"""

from __future__ import annotations

import json
import re
from typing import Any


class WorkflowYAMLError(ValueError):
    pass


def _strip_comment(text: str) -> str:
    quote = ""
    escaped = False
    depth = 0
    index = 0
    while index < len(text):
        char = text[index]
        if quote == '"':
            if escaped:
                escaped = False
            elif char == "\\":
                escaped = True
            elif char == quote:
                quote = ""
            index += 1
            continue
        elif quote == "'":
            if char == quote:
                if index + 1 < len(text) and text[index + 1] == quote:
                    index += 2
                    continue
                quote = ""
            index += 1
            continue
        if char in {'"', "'"}:
            quote = char
        elif char in "[{":
            depth += 1
        elif char in "]}":
            depth -= 1
            if depth < 0:
                raise WorkflowYAMLError("unbalanced flow delimiter")
        elif char == "#" and depth == 0 and (
            index == 0 or text[index - 1].isspace()
        ):
            return text[:index].rstrip()
        index += 1
    if quote or depth:
        raise WorkflowYAMLError("unterminated quote or flow collection")
    return text.rstrip()


def _plain_scalar(text: str) -> Any:
    value = text.strip()
    lowered = value.lower()
    if lowered == "true":
        return True
    if lowered == "false":
        return False
    if lowered in {"null", "~"}:
        return None
    if re.fullmatch(r"-?(?:0|[1-9][0-9]*)", value):
        return int(value)
    if not value or value[0] in "!&*":
        raise WorkflowYAMLError(f"unsupported or empty scalar: {value!r}")
    return value


class _FlowParser:
    def __init__(self, text: str):
        self.text = text
        self.index = 0

    def parse(self) -> Any:
        value = self._value()
        self._space()
        if self.index != len(self.text):
            raise WorkflowYAMLError(
                f"unexpected flow content: {self.text[self.index:]!r}"
            )
        return value

    def _space(self) -> None:
        while self.index < len(self.text) and self.text[self.index].isspace():
            self.index += 1

    def _value(self) -> Any:
        self._space()
        if self.index >= len(self.text):
            raise WorkflowYAMLError("missing flow value")
        char = self.text[self.index]
        if char == "{":
            return self._mapping()
        if char == "[":
            return self._sequence()
        if char == '"':
            return self._double_quoted()
        if char == "'":
            return self._single_quoted()
        return self._plain({",", "]", "}"})

    def _double_quoted(self) -> str:
        start = self.index
        self.index += 1
        escaped = False
        while self.index < len(self.text):
            char = self.text[self.index]
            self.index += 1
            if escaped:
                escaped = False
            elif char == "\\":
                escaped = True
            elif char == '"':
                try:
                    return json.loads(self.text[start : self.index])
                except json.JSONDecodeError as error:
                    raise WorkflowYAMLError(str(error)) from error
        raise WorkflowYAMLError("unterminated double-quoted scalar")

    def _single_quoted(self) -> str:
        self.index += 1
        parts: list[str] = []
        while self.index < len(self.text):
            char = self.text[self.index]
            self.index += 1
            if char != "'":
                parts.append(char)
                continue
            if self.index < len(self.text) and self.text[self.index] == "'":
                parts.append("'")
                self.index += 1
                continue
            return "".join(parts)
        raise WorkflowYAMLError("unterminated single-quoted scalar")

    def _plain(self, delimiters: set[str]) -> Any:
        start = self.index
        while self.index < len(self.text) and self.text[self.index] not in delimiters:
            self.index += 1
        return _plain_scalar(self.text[start : self.index])

    def _key(self) -> str:
        self._space()
        if self.index >= len(self.text):
            raise WorkflowYAMLError("missing flow mapping key")
        if self.text[self.index] == '"':
            key = self._double_quoted()
        elif self.text[self.index] == "'":
            key = self._single_quoted()
        else:
            start = self.index
            while self.index < len(self.text) and self.text[self.index] != ":":
                if self.text[self.index] in "{},[]":
                    raise WorkflowYAMLError("invalid flow mapping key")
                self.index += 1
            key = str(_plain_scalar(self.text[start : self.index]))
        if not isinstance(key, str) or not key:
            raise WorkflowYAMLError("mapping keys must be non-empty strings")
        return key

    def _mapping(self) -> dict[str, Any]:
        self.index += 1
        result: dict[str, Any] = {}
        self._space()
        if self.index < len(self.text) and self.text[self.index] == "}":
            self.index += 1
            return result
        while True:
            key = self._key()
            self._space()
            if self.index >= len(self.text) or self.text[self.index] != ":":
                raise WorkflowYAMLError("flow mapping key is missing ':'")
            self.index += 1
            if key in result:
                raise WorkflowYAMLError(f"duplicate mapping key: {key}")
            result[key] = self._value()
            self._space()
            if self.index >= len(self.text):
                raise WorkflowYAMLError("unterminated flow mapping")
            char = self.text[self.index]
            self.index += 1
            if char == "}":
                return result
            if char != ",":
                raise WorkflowYAMLError("flow mapping entries must be comma-separated")

    def _sequence(self) -> list[Any]:
        self.index += 1
        result: list[Any] = []
        self._space()
        if self.index < len(self.text) and self.text[self.index] == "]":
            self.index += 1
            return result
        while True:
            result.append(self._value())
            self._space()
            if self.index >= len(self.text):
                raise WorkflowYAMLError("unterminated flow sequence")
            char = self.text[self.index]
            self.index += 1
            if char == "]":
                return result
            if char != ",":
                raise WorkflowYAMLError("flow sequence entries must be comma-separated")


def _inline(text: str) -> Any:
    value = _strip_comment(text).strip()
    if value.startswith(("{", "[", '"', "'")):
        return _FlowParser(value).parse()
    return _plain_scalar(value)


def _split_mapping_entry(text: str) -> tuple[str, str]:
    quote = ""
    escaped = False
    depth = 0
    index = 0
    while index < len(text):
        char = text[index]
        if quote == '"':
            if escaped:
                escaped = False
            elif char == "\\":
                escaped = True
            elif char == quote:
                quote = ""
            index += 1
            continue
        elif quote == "'":
            if char == quote:
                if index + 1 < len(text) and text[index + 1] == quote:
                    index += 2
                    continue
                quote = ""
            index += 1
            continue
        if char in {'"', "'"}:
            quote = char
        elif char in "[{":
            depth += 1
        elif char in "]}":
            depth -= 1
        elif char == ":" and depth == 0:
            return text[:index].strip(), text[index + 1 :].strip()
        index += 1
    if quote or depth:
        raise WorkflowYAMLError("unterminated mapping entry")
    raise WorkflowYAMLError(f"mapping entry is missing ':': {text!r}")


class _BlockParser:
    def __init__(self, text: str):
        if "\t" in text:
            raise WorkflowYAMLError("tabs are not allowed in workflow YAML")
        self.lines = text.splitlines()
        self.index = 0

    def parse(self) -> Any:
        peek = self._peek()
        if peek is None:
            raise WorkflowYAMLError("empty workflow")
        _, indent, _ = peek
        if indent != 0:
            raise WorkflowYAMLError("workflow root must start at indentation zero")
        result = self._block(0)
        if self._peek() is not None:
            raise WorkflowYAMLError("trailing workflow content")
        return result

    def _line(self, index: int) -> tuple[int, str]:
        raw = self.lines[index]
        indent = len(raw) - len(raw.lstrip(" "))
        return indent, _strip_comment(raw[indent:])

    def _peek(self) -> tuple[int, int, str] | None:
        index = self.index
        while index < len(self.lines):
            indent, content = self._line(index)
            if content:
                return index, indent, content
            index += 1
        return None

    def _consume(self) -> tuple[int, str]:
        peek = self._peek()
        if peek is None:
            raise WorkflowYAMLError("unexpected end of workflow")
        index, indent, content = peek
        self.index = index + 1
        return indent, content

    def _block(self, indent: int) -> Any:
        peek = self._peek()
        if peek is None or peek[1] != indent:
            raise WorkflowYAMLError(f"expected block at indentation {indent}")
        if peek[2] == "-" or peek[2].startswith("- "):
            return self._sequence(indent)
        return self._mapping(indent)

    def _key(self, text: str) -> str:
        value = _inline(text)
        if not isinstance(value, str) or not value:
            raise WorkflowYAMLError("mapping keys must be non-empty strings")
        return value

    def _value(self, raw: str, parent_indent: int) -> Any:
        if raw in {"|", "|-", ">", ">-"}:
            return self._block_scalar(parent_indent, raw)
        if raw:
            return _inline(raw)
        peek = self._peek()
        if peek is None or peek[1] <= parent_indent:
            return None
        return self._block(peek[1])

    def _put(self, result: dict[str, Any], key: str, value: Any) -> None:
        if key in result:
            raise WorkflowYAMLError(f"duplicate mapping key: {key}")
        result[key] = value

    def _mapping(
        self, indent: int, initial: dict[str, Any] | None = None
    ) -> dict[str, Any]:
        result = {} if initial is None else initial
        while True:
            peek = self._peek()
            if peek is None or peek[1] != indent:
                return result
            _, _, content = peek
            if content == "-" or content.startswith("- "):
                return result
            _, content = self._consume()
            key_text, raw = _split_mapping_entry(content)
            key = self._key(key_text)
            self._put(result, key, self._value(raw, indent))

    def _sequence(self, indent: int) -> list[Any]:
        result: list[Any] = []
        while True:
            peek = self._peek()
            if peek is None or peek[1] != indent:
                return result
            _, _, content = peek
            if content != "-" and not content.startswith("- "):
                return result
            _, content = self._consume()
            raw = content[1:].strip()
            if not raw:
                nested = self._peek()
                if nested is None or nested[1] <= indent:
                    result.append(None)
                else:
                    result.append(self._block(nested[1]))
                continue
            if raw.startswith(('{', '[', '"', "'")):
                result.append(_inline(raw))
                continue
            try:
                key_text, value_text = _split_mapping_entry(raw)
            except WorkflowYAMLError:
                result.append(_inline(raw))
                continue
            item: dict[str, Any] = {}
            key = self._key(key_text)
            self._put(item, key, self._value(value_text, indent + 2))
            peek = self._peek()
            if peek is not None and peek[1] == indent + 2:
                item = self._mapping(indent + 2, item)
            result.append(item)

    def _block_scalar(self, parent_indent: int, marker: str) -> str:
        start = self.index
        content_indexes: list[int] = []
        index = start
        while index < len(self.lines):
            raw = self.lines[index]
            if not raw.strip():
                content_indexes.append(index)
                index += 1
                continue
            indent = len(raw) - len(raw.lstrip(" "))
            if indent <= parent_indent:
                break
            content_indexes.append(index)
            index += 1
        self.index = index
        nonblank = [
            len(self.lines[i]) - len(self.lines[i].lstrip(" "))
            for i in content_indexes
            if self.lines[i].strip()
        ]
        if not nonblank:
            return ""
        content_indent = min(nonblank)
        lines = [
            self.lines[i][content_indent:] if self.lines[i].strip() else ""
            for i in content_indexes
        ]
        while lines and not lines[-1]:
            lines.pop()
        if marker.startswith(">"):
            value = " ".join(lines)
        else:
            value = "\n".join(lines)
        return value if marker.endswith("-") else value + "\n"


def loads(text: str) -> Any:
    return _BlockParser(text).parse()
