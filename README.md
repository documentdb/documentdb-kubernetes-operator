# DocumentDB Kubernetes Operator

A Kubernetes operator for managing DocumentDB clusters in your Kubernetes environment. This operator provides a native Kubernetes way to deploy, manage, and scale DocumentDB instances with MongoDB-compatible API.

## 🚀 What is DocumentDB Kubernetes Operator?

The DocumentDB Kubernetes Operator extends Kubernetes with Custom Resource Definitions (CRDs) to manage DocumentDB clusters declaratively. It leverages the power of Kubernetes controllers to ensure your DocumentDB deployments are always in the desired state.

### Key Features

- **Declarative Management**: Define your DocumentDB clusters using Kubernetes manifests
- **Automated Operations**: Automatic deployment, scaling, and lifecycle management
- **MongoDB Compatibility**: MongoDB API–compatible for seamless integration
- **Cloud Native**: Built on CloudNative-PG for robust PostgreSQL foundation
- **Helm Chart Support**: Easy installation and configuration via Helm
- **Enterprise Grade**: Multi-cloud support and high availability

## 📚 Documentation

For installation guides, configuration options, and examples, visit our [documentation](https://documentdb.io/documentdb-kubernetes-operator/latest/preview/).

## 🚀 Quick Start

Ready to get started? Check out our [Quick Start Guide](https://documentdb.io/documentdb-kubernetes-operator/latest/preview/#quickstart) for step-by-step instructions to deploy your first DocumentDB cluster in minutes.

## Development Setup

For information on setting up your development environment to contribute to this project, see our [Developer Guide](docs/developer-guides/development-environment.md).

## 🌐 Cloud Platform Setup Guides

Deploy DocumentDB clusters across different cloud platforms and configurations:

- **Azure (AKS)**: [Comprehensive deployment automation scripts for Azure Kubernetes Service](documentdb-playground/aks-setup/README.md)
- **AWS (EKS)**: [Simple automation scripts for deploying on Amazon Elastic Kubernetes Service](documentdb-playground/aws-setup/README.md)
- **Multi-Cloud**: [High availability setup across multiple cloud providers using KubeFleet and Istio](documentdb-playground/multi-cloud-deployment/README.md)
- **Azure Multi-Region (AKS + AzureFleet)**: [Deployment scripts for deploying multi-regionally in Azure](documentdb-playground/aks-fleet-deployment/README.md)
- **TLS Configuration**: [Gateway TLS setup with multiple certificate modes (self-signed, provided, cert-manager)](documentdb-playground/tls/README.md)
- **Operator Upgrades**: [Local development guide for testing operator control plane upgrades](documentdb-playground/operator-upgrade-guide/README.md)

## 📄 License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.

## 🔒 Security

For security concerns, please review our [Security Policy](SECURITY.md).

## 💬 Support

- Create an [issue](https://github.com/documentdb/documentdb-kubernetes-operator/issues) for bug reports and feature requests
- Check our [documentation](https://documentdb.io/documentdb-kubernetes-operator/latest/preview/) for common questions
- [Join our Discord](https://discord.gg/vH7bYu524D) and then contact us on [#kubernetes](https://discordapp.com/channels/1374170121219866635/1435045191156236458)
