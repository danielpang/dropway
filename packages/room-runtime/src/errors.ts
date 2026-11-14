// SPDX-License-Identifier: FSL-1.1-Apache-2.0

export class RoomRuntimeError extends Error {
  constructor(
    readonly code: string,
    message: string,
  ) {
    super(message);
    this.name = "RoomRuntimeError";
  }
}

export function fail(code: string, message: string): never {
  throw new RoomRuntimeError(code, message);
}
