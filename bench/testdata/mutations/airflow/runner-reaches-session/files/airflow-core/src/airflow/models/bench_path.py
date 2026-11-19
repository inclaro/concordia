from airflow.utils.session import create_session


def bench_helper():
    return create_session()
