Design: VDPA device management
------------------------------

**Status:** Draft
**Date:** 2026/09/03
**Authors:** bgartzi

# Overview

This document covers the changes needed in the SR-IOV DRA driver to be
able to manage VDPA devices onto SR-IOV virtual functions.  It also
proposes changing the current VDPA device lifecycle architecture, taking
VDPA device creation out of the sriov-network-operator and bringing it
to device allocation time, which improves hardware pool allocation
flexibility.

# Motivation

The sriov-network-operator and sriov-network-device-plugin are able to
manage, advertise and allocate hardware VDPA devices.  Implementing that
into the SR-IOV DRA driver brings feature parity closer.

Moreover, the allocation API that DRA brings larger flexibility in
comparison to the device-plugin architecture.  With this API, VDPA
devices can be created and removed during allocation and deallocation.
Bringing the creation and removal logic into allocation time permits
managing a pool of SR-IOV devices that are not pre-assigned to VDPA
devices, which lets pods share the same pool and policy, yet, be
assigned VFs or VDPA devices based on their specific needs.

# Goals
- Bring VDPA device management to allocation/deallocation time.
- Advertise VDPA device attributes.
- Allocate vhost and virtio VDPA devices to pods dynamically.

# Non-Goals
- Changes to CDI, CNI or NRI integration to support VDPA devices.

# Design details

## Required API changes
### ResourceSlice attribute extension: exposing device vdpa capabilities
When bringing VDPA device creation to device allocation time, the DRA
driver needs to distinguish VFs that are able to support such workloads.
This comes down to three parameters:
- Support for vdpa (boolean).
- Supported virtio features (uint64 bitmap).
- Supported maximum virt-queues (uint32 value).

This values need to be reported by the DRA driver, so the user can use
`ResourceClaim` attribute filtering to ensure that assigned VFs support
the desired VDPA device configuration.

VDPA capability and maximum virt-queues are easy to report and filter.
Those are quantitative values that can be easily filtered out through
CEL expressions in a `ResourceClaim`. This design proposes exposing them
under the following `ResourceSlice` attributes:
- `sriovnetwork.k8snetworkplumbingwg.io/vdpaCapable` -> `BoolValue`.
- `sriovnetwork.k8snetworkplumbingwg.io/maxSupportedVQs` Contains a
  `StringValue` containing a `uint32`, although it is also safe and
  sound to store it in an `IntValue`, which has 64 bit capacity, enough
  to store a `uint32` value.

However, when it comes to virtio-features the thing gets more
complicated. It is a `uint64` value, but it is not quantitative, but a
bitmap. Currently, kubernetes CEL expressions do not support bit
operations. That's why right now it is proposed to run virtio feature
filtering at `SriovResourcePolicy` level: we can implement such
bit-level logic in the resource policy controller. The attribute,
however, is also exposed as `ResourceSlice` attributes, right under:
- `sriovnetwork.k8snetworkplumbingwg.io/virtioFeatures`: It contains a
  string with the value's hexadecimal encoding. However, `BoolValues`
  could also help this task, improving transparency for the sake of
  bloatness and difficulty for encoding/decoding.

Using a `BoolValues` instead of a string for `virtioFeatures` does not
seem to improve the thing. Yes, it would contain all the filtering logic
in the CEL expression, yet make it hard to write and decypher. In other
words, the users would need to be really familiar with virtio features,
knowing how to translate bit position to feature meaning.

### ResourcePolicy extensions: Being able to filter virtio features
A user might want to make sure that assigned devices support the
required virtio-features. This design proposes extending the
`ResourcePolicy` API to accept a `uint64` with the minimum required
virtio features. Then, the resource policy controller will be able to
filter out those devices that do not match the minimum requirements.

**NOTE**: Honestly, this could be way better than this, and move this
filtering logic to the CEL expressions logic that `vdpaCapable` and
`maxSupportedVQs` will use. This was put this way for the sake of
simplicity when it comes to writing filters for virtio features, as CEL
expressions do not support bit-level operations as far as I'm concerned,
although using `BoolValues` might be a valid choice. Summary: this is
very discussable.

### ResourceClaim opaque VFConfig extension: Letting users claim VDPA devices
Exposing VF VDPA capabilities lets users filter those devices that match
their workload requirements. However, they also need to provide details
about the configuration that they want to achieve. There are 4 key
configuration parameters, assuming MAC address will be managed by the
CNI (out of the scope of this design):
- Driver: `virtio_vdpa` or `vhost_vdpa`.
- MTU: `uint16` value.
- Virtqueues: `uint16` value.
- Virtio Features: `uint64` bitmap.

This design proposes to extend `VfConfig` with a new field called `Vdpa`
which will hold a pointer to a `VdpaConfig` structure:
```json
{
    "driver": "default",
    "addVhostMount": false,
    "vdpa": {
        "driver": "vhost_vdpa",
        "mtu": 9000,
        "maxVQP": 32,
        "virtioFeatureBits": 111111
    }
}
```

As soon as a the configuration contains VDPA configuration and specifies
a valid VPDA driver, the SR-IOV DRA driver will create a VDPA device
during allocation, that will be tied to the pod's lifecycle. In other
words, the VDPA device will be removed and unassigned from the VF once
the pod is over.

#### Alternatives to configuration
We could just wait for a vdpa-related driver in the main `driver`
section of the structure, then trigger the VDPA allocation mechanism
based on that, instead of checking if the vdpa structure contains `nil`
or points into an actual structure.

## Device filtering

### ResourcePolicy level filtering
It is proposed for the `ResourcePolicy` to hold a `virtioFeatures`
`uint64` value that users can use to define pools of devices that meet
a minimum set of virtio features.

**NOTE**: This could be put in a better way into `ResourceClaim`
filtering through CEL expressions. As mentioned before, k8s CEL
expressions do not support bit-level operations/masking, which makes
this harder, unless a `BoolValues` list is used, which would make
`ResourceSlices` look a bit "*bloaty*". It would help keep all filters
fot VDPA in one place. However, this design was put the other way
around, for ease of feature parity check implementation, and that's why
it is mentioned a few times in this document that this is one BIG point
of discussion and that feedback will be highly appreciated.

### ResourceClaim level filtering
Devices that are `vdpaCapable` can be filtered in `ResourceClaim`s by
CEL expressions that check such attribute of `ResourceSlice`s.

The same road can be followed to filter devices that match a minimum
amount of supported maximum virtqueues, by running a CEL check against
device `maxSupportedVQs` attribute.


## Required internal changes
- Internal pkg/host/host.go refactorings including:
    · Low level by-bus ops not to affect only pci, but also vdpa.
- Host Interface changes to generalize some of the device ops.
- Host Interface extension to support a broader range of operations.
- Kernel module loading logic deduplication.
- Vdpa providers + host helpers.

# Alternatives

## Avoid device lifecycle management (just advertise and allocate)
The main alternative to the design proposed above is to limit the SR-IOV
DRA driver to the functionalities that the sriov-network-device-plugin
covered.  That is, finding VDPA devices that already exist and match the
lookup filters, advertise those and allocate them for the target pods.
Leaving the job of creating them to the sriov-network-operator ahead of
allocation time.  In other words, replicating the workflow that exists
for VFs for VDPA devices.

This would also require exposing some new device attributes to
`ResourceSlice`s, which would not specify device capabilities, but
rather strong pre-configured device constraints. The user could use CEL
expressions in their `ResourceClaim`s to match those devices that are
pre-assigned a VDPA device.

Changes to `VfConfig` or `SriovResourcePolicy` would not be necessary
for this alternative.

In summary, it would be simpler from a configuration perspective, for
the sake of pool flexibility.
