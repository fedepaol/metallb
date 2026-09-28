# Project: MetalLB

## Overview
MetalLB is a load-balancer implementation for bare-metal Kubernetes clusters, using standard routing protocols. For detailed architecture and design information, see website/content.

## Tech Stack
- **Language:** Go
- **BGP Implementation:** FRR (Free Range Routing) - https://frrouting.org/ (and native Go BGP)
- **Platform:** Kubernetes

## Development Workflow

### Project Structure

Project layout, build commands, and test instructions are documented in @website/content/concepts and on https://metallb.io/community.

### Coding style

Follow the coding guidelines in @CODING_STYLE.md — read it before writing or reviewing code.

### Testing Strategy

**Unit Tests:**
Run `inv test` to execute unit tests.

**Linting**
Run `inv lint` to lint the code.

**End-to-End Tests:**
E2E tests live in `/e2etest`. See the contributing docs for how to deploy a dev cluster and run the suite.
