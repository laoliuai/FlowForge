from setuptools import setup, find_packages


setup(
    name="flowforge",
    version="0.1.0",
    description="FlowForge Python SDK",
    package_dir={"": "."},
    packages=find_packages("."),
    install_requires=["grpcio", "redis", "requests"],
    python_requires=">=3.8",
)
