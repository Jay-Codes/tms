'use client';

/**
 * Setup wizard step registry (FLOWS flow 1 step 3).
 *
 * Each step body lives in its own file under ./steps/ so later phases can fill
 * them in independently. A step whose body is not built yet states what it will
 * collect; `built: true` marks the ones that are real screens now.
 */

import type { ComponentType } from 'react';
import { BrandingStep } from './steps/BrandingStep';
import { FirstPropertyStep } from './steps/FirstPropertyStep';
import { PaymentPeriodsStep } from './steps/PaymentPeriodsStep';
import { UnitsStep } from './steps/UnitsStep';
import { ContractTemplateStep } from './steps/ContractTemplateStep';
import { NotificationsStep } from './steps/NotificationsStep';

export interface WizardStep {
  id: string;
  label: string;
  /** Phase in which this step's body gets built. */
  phase: number;
  /** True once the body is a real, working screen. */
  built?: boolean;
  Body: ComponentType;
}

export const STEPS: WizardStep[] = [
  { id: 'branding', label: 'Branding', phase: 4, built: true, Body: BrandingStep },
  { id: 'property', label: 'First property', phase: 2, built: true, Body: FirstPropertyStep },
  { id: 'periods', label: 'Payment periods', phase: 2, built: true, Body: PaymentPeriodsStep },
  { id: 'units', label: 'Units', phase: 2, built: true, Body: UnitsStep },
  { id: 'template', label: 'Contract template', phase: 4, built: true, Body: ContractTemplateStep },
  { id: 'notifications', label: 'Notifications', phase: 6, Body: NotificationsStep },
];
