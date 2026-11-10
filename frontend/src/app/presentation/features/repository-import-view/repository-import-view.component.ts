import { ChangeDetectionStrategy, Component, OnInit, inject } from '@angular/core';
import { RepositoryImportStore } from '../../../application/stores/repository-import.store';
import { WorkspaceStore } from '../../../application/stores/workspace.store';
import { TranslatePipe } from '../../../infrastructure/i18n/json-translation.gateway';
import { PageHeaderComponent } from '../../common/page-header/page-header.component';

/** Vercel-style "Import repository": pick an account where the app is installed, follow repositories. */
@Component({
  selector: 'app-repository-import-view',
  standalone: true,
  imports: [PageHeaderComponent, TranslatePipe],
  templateUrl: './repository-import-view.component.html',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class RepositoryImportViewComponent implements OnInit {
  readonly importStore = inject(RepositoryImportStore);
  private readonly workspace = inject(WorkspaceStore);

  ngOnInit(): void {
    void this.importStore.open();
  }

  onAccountChange(event: Event): void {
    void this.importStore.selectAccount(Number((event.target as HTMLSelectElement).value));
  }

  onSearch(event: Event): void {
    this.importStore.setQuery((event.target as HTMLInputElement).value);
  }

  back(): void {
    this.workspace.setPerspective('seats');
  }
}
