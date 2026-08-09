from setuptools import setup, find_packages

setup(
    name="aegis-proxy-sdk",
    version="0.2.1-rc.1",
    packages=find_packages(),
    install_requires=[
        "psycopg2-binary",
        "requests",
        "langchain-community",
        "sseclient-py",
        "PyJWT",
    ],
    author="Raymond Hani",
    description="Aegis AI-Native DB Proxy Python SDK",
    classifiers=[
        "Programming Language :: Python :: 3",
        "License :: OSI Approved :: MIT License",
    ],
)
