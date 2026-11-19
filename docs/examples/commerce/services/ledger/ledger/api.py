from . import store


def post_entry(account: str, cents: int) -> int:
    return store.append_entry(account, cents)
