'use client';

/**
 * Setup wizard step registry (FLOWS flow 1 step 3).
 *
 * Each step body lives in its own file under ./steps/ so later phases can fill
 * them in independently. In Phase 1 every body only states what the step will
 * collect; navigation and progress persistence are real.
 *
 * Step 3 (payment periods) is explicitly NOT built in this phase.
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
  Body: ComponentType;
}

export const STEPS: WizardStep[] = [
  { id: 'branding', label: 'Branding', phase: 8, Body: BrandingStep },
  { id: 'property', label: 'First property', phase: 2, Body: FirstPropertyStep },
  { id: 'periods', label: 'Payment periods', phase: 2, Body: PaymentPeriodsStep },
  { id: 'units', label: 'Units', phase: 2, Body: UnitsStep },
  { id: 'template', label: 'Contract template', phase: 4, Body: ContractTemplateStep },
  { id: 'notifications', label: 'Notifications', phase: 6, Body: NotificationsStep },
];
