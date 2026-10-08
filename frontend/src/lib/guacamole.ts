// guacamole-common-js ships a single default export at runtime, while its
// community types describe named exports. Bridge the two here, once.
import type * as GuacamoleTypes from 'guacamole-common-js';
// @ts-ignore -- runtime default export not described by the types
import runtime from 'guacamole-common-js';

export const Guacamole = runtime as typeof GuacamoleTypes;
export type { GuacamoleTypes };
