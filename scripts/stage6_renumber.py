#!/usr/bin/env python3
"""Stage 6: remove internal step/phase numbering from comments and docs.

SAFE version: only rewrites the COMMENT part of each line (never code,
never leading whitespace). Legitimate terms preserved: «فاز اتصال»,
«گام‌به‌گام».
"""
import glob
import re

STEP = re.compile(
    r'(?:\(\s*)?(?:گام|فاز)\s*[۰-۹0-9]+(?:\s*b)?(?:\s*-\s*[AaА]\s*[0-9]+)?'
    r'(?:\s*[،,)؛]|\)|(?=\s))?'
)
CODE = re.compile(r'\b[۰-۹0-9]+b-[AaА][0-9]+\b\s*[:：]?\s*|\b[AaА][0-9]\b(?=\s*[-—:(])')
SAFE = re.compile(r'فاز اتصال|گام‌به‌گام|گام به گام')


def comment_split(line):
    """Split a line into (code, comment) at a // that is outside strings."""
    in_str = in_char = escape = False
    for i, ch in enumerate(line):
        if escape:
            escape = False
            continue
        if ch == '\\' and (in_str or in_char):
            escape = True
            continue
        if in_str:
            if ch == '"':
                in_str = False
            continue
        if in_char:
            if ch == "'":
                in_char = False
            continue
        if ch == '"':
            in_str = True
        elif ch == "'":
            in_char = True
        elif ch == '/' and i + 1 < len(line) and line[i + 1] == '/':
            return line[:i], line[i:]
    return line, ''


def clean_comment(text):
    placeholders = []

    def stash(m):
        placeholders.append(m.group(0))
        return '\x00%d\x00' % (len(placeholders) - 1)

    text = SAFE.sub(stash, text)
    text = STEP.sub('', text)
    text = CODE.sub('', text)
    text = re.sub(r'\(\s*[,،;؛]\s*', '(', text)
    text = re.sub(r'\(\s*\)', '', text)
    text = re.sub(r'\(\s*—\s*', '(', text)
    text = re.sub(r'\s+—\s*\)', ')', text)
    text = re.sub(r'[ \t]{2,}', ' ', text)        # collapse double spaces inside comment text
    text = re.sub(r'\s+([.،؛:!؟])', r'\1', text)  # dangling space before punctuation
    text = re.sub(r'^(\s*//)[ \t]+', r'\1 ', text)
    text = re.sub(r'\x00(\d+)\x00', lambda m: placeholders[int(m.group(1))], text)
    return text


def process_line(line):
    if '//' not in line:
        return line
    code, comment = comment_split(line)
    new_comment = clean_comment(comment)
    if new_comment == comment:
        return line
    return code + new_comment


def process_md_line(line):
    stripped = line.lstrip()
    if not stripped:
        return line
    if not (STEP.search(stripped) or CODE.search(stripped)):
        return line
    lead = line[:len(line) - len(stripped)]
    return lead + clean_comment(stripped)


def main():
    for path in sorted(glob.glob('*.go')):
        lines = open(path).read().split('\n')
        out = [process_line(ln) for ln in lines]
        if out != lines:
            open(path, 'w').write('\n'.join(out))
            print('rewrote', path)
    for path in ['README.md', 'ARCHITECTURE.md', 'BUGFIXES.md']:
        try:
            lines = open(path).read().split('\n')
        except OSError:
            continue
        out = [process_md_line(ln) for ln in lines]
        if out != lines:
            open(path, 'w').write('\n'.join(out))
            print('rewrote', path)


if __name__ == '__main__':
    main()
