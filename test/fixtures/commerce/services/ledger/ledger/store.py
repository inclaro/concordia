_entries: list[tuple[str, int]] = []


def append_entry(account: str, cents: int) -> int:
    _entries.append((account, cents))
    return len(_entries)
