# Split FRR - Proposal to move FRR to a stand alone component

## Summary

MetalLB relies on FRR under the hood, which has way more to offer than "just"
announcing prefixes as MetalLB does. Here we propose to split MetalLB and create
a new, possibly standalone, FRR daemonset with its own API which can be alimented
both by MetalLB but also by other actors.

## Motivation

There are users who need running FRR (or an alternative implementation) on the nodes for other purpouses, receiving routes from their routers being the
most popular one.

They require receiving routes via BGP for multiple reasons:

- They want to be able to inject routes coming dynamically from other clusters
- When enabling anycast gw in some routers, then bgp peering does not work anymore. Because of this, they require ecmp routes to be able to establish HA in their egress path
- Having a way to configure multiple DCGWs in an active - active fashion allows a better balance of the egress traffic and a quicker failure detection with BFD

MetalLB is meant to announce routes (in particular, to reach Services of type LoadBalancer) only, so it is definetely not the right place to implement a broader FRR configuration.

At the same time, the approach of having a single FRR instance is optimal for both performances and for limiting
the number of open sessions (see more on the _alternatives_ section).

## Goals

- Having a separate FRR component where the user can add its own part of the configuration
- Allowing multiple users (or controllers) to contribute to the configuration in an incremental manner
- Having a cloud native API that allows configuring the subset of the FRR features we want to expose
- Offer a way to experimenting allowing a "do it at your own risk" way to set a raw FRR configuration
- Allowing multiple nodes to have different FRR configurations
- Replacing the FRR based MetalLB implementation with a layer that aliments the API of this new daemon
- Allowing the deployment of this new daemon as a stand alone component not
necessarily tied to MetalLB

## Non Goals

- Covering all the possible FRR configurations. We will start with a limited
API that can be expanded depending on the use case
- Exposing the raw FRR configuration as the way of configuring the daemon. The raw config must serve only as an experimentation tool
- Allowing a configuration to remove the configuration added by another actor. This might not always be possible, but when designing the API, this should always be taken in consideration

## Proposal

### User Stories

#### Story 1

As a cluster administrator, I want to continue using MetalLB with the current allowed API.

#### Story 2

As a cluster administrator, I want to allow FRR to receive routes, but only for a specific prefix.

#### Story 3

As a cluster administrator, I want to deploy only FRR to receive routes, connecting to different peers depending on the nodes.

## Design details

The idea is to have a daemonset, with the pod running on each node, whose pods
have the same structure the speaker pod has today (frr container, reloader, metrics, etc).

The speaker will be provided with a new `frrk8s` bgp mode which will translate the MetalLB api to the new controller's api.
Given that the new api will allow setting the configuration per node basis, each speaker will take care of configuring its own node.

### The API

Before describing the options, we are going to list the properties we want from the API exposed by the FRR Daemonset:

- It must be possible to set a specific configuration per node. In particular, each metallb speaker will configure the instance running on its node
- It must be possible to apply the metallb configuration and the user’s configuration over the same FRR instance (and ideally, any configuration applied by any external component)

Additionally, we may want to enable the following scenarios:

- A user may want to connect to another neighbor
- A user may want to advertise an extra set of IPs for a given neighbor (i.e. the pod’s CIDR)
- A user may want to override the configuration put in place by MetalLB to reject the incoming routes

In order to provide an abstraction of the FRR Configuration, we need to represent the following entities:

The router:

- ASN
- ID
- VRF

For each router, we must specify a neighbor with all the details we have in a session:

- IP
- ASN
- passwords
- bfd profile (if enabled)
- the ips we want to advertise

For each neighbor we must specify the route-map entries, and in particular:

- the list of prefixes we allow FRR to advertise
- the list of prefixes we allow FRR to receive
- the list of prefixes we would set the community to
- the list of prefixes we would set a localpref to when announcing

If we split the configuration in multiple sub-entities (router, neighbor for router, allowed ips for any neighbor), the configuration of a single node becomes spread across multiple rows of multiple entities.

Because of this, a single CRD with substructures is going to provide better
readability and it's going to be easier to maintain.

## TODO
lifecycle
ensure metallb runs the right version
