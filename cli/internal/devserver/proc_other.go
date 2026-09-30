// SPDX-License-Identifier: Apache-2.0

//go:build !linux

package devserver

import "syscall"

func sysProcAttr() *syscall.SysProcAttr { return nil }
