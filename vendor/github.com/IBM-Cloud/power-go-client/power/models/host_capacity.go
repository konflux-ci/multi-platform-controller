// Code generated by go-swagger; DO NOT EDIT.

package models

// This file was generated by the swagger tool.
// Editing this file might prove futile when you re-run the swagger generate command

import (
	"context"

	"github.com/go-openapi/errors"
	"github.com/go-openapi/strfmt"
	"github.com/go-openapi/swag"
)

// HostCapacity host capacity
//
// swagger:model HostCapacity
type HostCapacity struct {

	// Core capacity of the host
	Cores *HostResourceCapacity `json:"cores,omitempty"`

	// Memory capacity of the host (in GB)
	Memory *HostResourceCapacity `json:"memory,omitempty"`
}

// Validate validates this host capacity
func (m *HostCapacity) Validate(formats strfmt.Registry) error {
	var res []error

	if err := m.validateCores(formats); err != nil {
		res = append(res, err)
	}

	if err := m.validateMemory(formats); err != nil {
		res = append(res, err)
	}

	if len(res) > 0 {
		return errors.CompositeValidationError(res...)
	}
	return nil
}

func (m *HostCapacity) validateCores(formats strfmt.Registry) error {
	if swag.IsZero(m.Cores) { // not required
		return nil
	}

	if m.Cores != nil {
		if err := m.Cores.Validate(formats); err != nil {
			if ve, ok := err.(*errors.Validation); ok {
				return ve.ValidateName("cores")
			} else if ce, ok := err.(*errors.CompositeError); ok {
				return ce.ValidateName("cores")
			}
			return err
		}
	}

	return nil
}

func (m *HostCapacity) validateMemory(formats strfmt.Registry) error {
	if swag.IsZero(m.Memory) { // not required
		return nil
	}

	if m.Memory != nil {
		if err := m.Memory.Validate(formats); err != nil {
			if ve, ok := err.(*errors.Validation); ok {
				return ve.ValidateName("memory")
			} else if ce, ok := err.(*errors.CompositeError); ok {
				return ce.ValidateName("memory")
			}
			return err
		}
	}

	return nil
}

// ContextValidate validate this host capacity based on the context it is used
func (m *HostCapacity) ContextValidate(ctx context.Context, formats strfmt.Registry) error {
	var res []error

	if err := m.contextValidateCores(ctx, formats); err != nil {
		res = append(res, err)
	}

	if err := m.contextValidateMemory(ctx, formats); err != nil {
		res = append(res, err)
	}

	if len(res) > 0 {
		return errors.CompositeValidationError(res...)
	}
	return nil
}

func (m *HostCapacity) contextValidateCores(ctx context.Context, formats strfmt.Registry) error {

	if m.Cores != nil {

		if swag.IsZero(m.Cores) { // not required
			return nil
		}

		if err := m.Cores.ContextValidate(ctx, formats); err != nil {
			if ve, ok := err.(*errors.Validation); ok {
				return ve.ValidateName("cores")
			} else if ce, ok := err.(*errors.CompositeError); ok {
				return ce.ValidateName("cores")
			}
			return err
		}
	}

	return nil
}

func (m *HostCapacity) contextValidateMemory(ctx context.Context, formats strfmt.Registry) error {

	if m.Memory != nil {

		if swag.IsZero(m.Memory) { // not required
			return nil
		}

		if err := m.Memory.ContextValidate(ctx, formats); err != nil {
			if ve, ok := err.(*errors.Validation); ok {
				return ve.ValidateName("memory")
			} else if ce, ok := err.(*errors.CompositeError); ok {
				return ce.ValidateName("memory")
			}
			return err
		}
	}

	return nil
}

// MarshalBinary interface implementation
func (m *HostCapacity) MarshalBinary() ([]byte, error) {
	if m == nil {
		return nil, nil
	}
	return swag.WriteJSON(m)
}

// UnmarshalBinary interface implementation
func (m *HostCapacity) UnmarshalBinary(b []byte) error {
	var res HostCapacity
	if err := swag.ReadJSON(b, &res); err != nil {
		return err
	}
	*m = res
	return nil
}