/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { act, renderHook, waitFor } from "@testing-library/react";
import { toast } from "sonner";
import { beforeEach, describe, expect, test, vi } from "vitest";

import { useSecureVerification } from "./use-secure-verification";

const apiMocks = vi.hoisted(() => ({
  checkVerificationMethods: vi.fn(),
  verify: vi.fn(),
}));

vi.mock("../api", () => apiMocks);

vi.mock("i18next", () => ({
  default: {
    t: (key: string) => key,
  },
}));

vi.mock("sonner", () => ({
  toast: {
    error: vi.fn(),
    info: vi.fn(),
    success: vi.fn(),
  },
}));

const passwordAndTwoFAMethods = {
  hasPassword: true,
  has2FA: true,
  hasPasskey: false,
  passkeySupported: false,
};

describe("useSecureVerification", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    apiMocks.checkVerificationMethods.mockResolvedValue(
      passwordAndTwoFAMethods,
    );
  });

  test("falls back when the preferred verification method is unavailable", async () => {
    const { result } = renderHook(() => useSecureVerification());

    await waitFor(() => {
      expect(apiMocks.checkVerificationMethods).toHaveBeenCalled();
    });

    await act(async () => {
      await result.current.startVerification(vi.fn(), {
        preferredMethod: "passkey",
      });
    });

    expect(result.current.currentMethod).toBe("2fa");
    expect(result.current.open).toBe(true);
  });

  test("reports method loading failures without treating them as unconfigured", async () => {
    apiMocks.checkVerificationMethods.mockResolvedValue(null);
    const onError = vi.fn();
    const { result } = renderHook(() => useSecureVerification({ onError }));

    await waitFor(() => {
      expect(apiMocks.checkVerificationMethods).toHaveBeenCalled();
    });

    let started = true;
    await act(async () => {
      started = await result.current.startVerification(vi.fn());
    });

    expect(started).toBe(false);
    expect(result.current.open).toBe(false);
    expect(toast.error).toHaveBeenCalledWith(
      "Failed to load verification methods",
    );
    expect(onError).toHaveBeenCalledWith(expect.any(Error));
    expect(toast.error).not.toHaveBeenCalledWith(
      "Set a password, Two-factor Authentication, or Passkey before proceeding",
    );
  });
});
