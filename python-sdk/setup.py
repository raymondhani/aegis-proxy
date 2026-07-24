from setuptools import setup, find_packages

setup(
    name="aegis-proxy-sdk",
    version="1.2.3",
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
