'use client';

/** One contract template: rich-text terms on the left, letterhead preview on the right. */

import { Icon } from '@iconify/react';
import Link from 'next/link';
import { useParams } from 'next/navigation';
import { useState } from 'react';
import { PageHead, Shell } from '../../../../components/Shell';
import { TemplateEditor } from '../../../../components/TemplateEditor';
import type { ContractTemplate } from '../../../../lib/api';

function TemplateBody({ id }: { id: string }) {
  const [template, setTemplate] = useState<ContractTemplate | null>(null);

  return (
    <>
      <PageHead
        title={template?.name || 'Template'}
        lead="Headings, lists and emphasis only — a contract is a document, not a web page."
        actions={
          <Link href="/contracts/templates" className="btn btn-quiet">
            <Icon icon="solar:arrow-left-linear" width={20} /> Templates
          </Link>
        }
      />
      <hr className="rule rule-strong" />
      <div style={{ paddingTop: 'var(--sp-5)' }}>
        <TemplateEditor templateId={id} onSaved={setTemplate} />
      </div>
    </>
  );
}

export default function TemplatePage() {
  const params = useParams<{ id: string }>();
  const id = String(params?.id ?? '');
  return (
    <Shell>
      <TemplateBody id={id} />
    </Shell>
  );
}
