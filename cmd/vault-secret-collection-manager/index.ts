interface secretCollection {
  name: string;
  path: string;
  members: string[];
}

function displayCreateSecretCollectionError(attemptedAction: string, msg: string) {
  const div = document.getElementById('modalError') as HTMLDivElement;
  div.innerText = `Failed to ${attemptedAction}: ${msg}`;
  div.classList.remove('hidden');
}

function clearCreateSecretCollectionError() {
  const div = document.getElementById('modalError') as HTMLDivElement;
  div.innerHTML = '';
  div.classList.add('hidden');
}

function hideModal() {
  document.getElementById('modalContainer')?.classList.add('hidden');
  clearCreateSecretCollectionError();
  const modalContent = document.getElementById('modalContent') as HTMLDivElement;
  for (const child of Array.from(modalContent.children)) {
    child.classList.add('hidden');
  }

  const currentMembersSelect = document.getElementById('currentMembersSelection') as HTMLSelectElement;
  for (const child of Array.from(currentMembersSelect.children)) {
    currentMembersSelect.removeChild(child);
  }

  const allUsersDataList = document.getElementById('all-members') as HTMLDataListElement;
  for (const child of Array.from(allUsersDataList.children)) {
    allUsersDataList.removeChild(child);
  }

  // We must detach edit member event handler, as it is scoped to a collection. It is not possible to loop over them,
  // so we remove _all_ event handlers on this layer: https://stackoverflow.com/a/35855487
  let updateMembersSubmitButton = document.getElementById('updateMemberSubmitButton') as HTMLButtonElement;
  updateMembersSubmitButton.parentElement.innerHTML = updateMembersSubmitButton.parentElement.innerHTML;
  document.getElementById('updateMemberCancelButton')?.addEventListener('click', () => hideModal());
}

function showModal() {
  document.getElementById('modalContainer')?.classList.remove('hidden');
}

function editMembersEventHandler(collection: secretCollection) {
  return function () {
    fetch(`${window.location.protocol}//${window.location.host}/users`)
      .then(async (response) => {
        let body = await response.text();
        if (!response.ok) {
          throw (body);
        }

        let allUsers: string[] = [];
        if (body !== '') {
          allUsers = JSON.parse(body);
        }

        let currentMembersSelect = document.getElementById("currentMembersSelection") as HTMLSelectElement;
        for (const existingMember of Array.from(collection.members)) {
          let existingMemberOption = document.createElement('option') as HTMLOptionElement;
          currentMembersSelect.appendChild(optionWithValue(existingMember));
        }

        let allMembersDatalist = document.getElementById('all-members') as HTMLDataListElement;
        for (const user of Array.from(allUsers)) {
          if (collection.members.includes(user)) {
            continue;
          }
          allMembersDatalist.appendChild(optionWithValue(user));
        }

        let removeMemberButton = document.getElementById('removeMemberButton') as HTMLButtonElement;
        removeMemberButton.addEventListener('click', () => {
          moveSelectedOptions(currentMembersSelect, allMembersDatalist, '');
        });

        let addMemberButton = document.getElementById('addMemberButton') as HTMLButtonElement;
        addMemberButton.addEventListener('click', () => {
          const allUsersInput = document.getElementById('allUsersInput') as HTMLInputElement;
          moveSelectedOptions(allMembersDatalist, currentMembersSelect, allUsersInput.value);
          allUsersInput.value = '';
        });

        document.getElementById('updateMemberSubmitButton')?.addEventListener('click', () => {
          const newValues = getSelectValues(document.getElementById('currentMembersSelection') as HTMLSelectElement);
          const body = JSON.stringify({ members: newValues });
          fetch(`${window.location.protocol}//${window.location.host}/secretcollection/${collection.name}/members`, { method: 'PUT', body: body })
            .then(async (response) => {
              if (!response.ok) {
                const responseText = await response.text();
                throw responseText;
              }
              fetchAndRenderSecretCollections();
              hideModal();
            })
            .catch((error) => {
              displayCreateSecretCollectionError('update members', error);
            });
        })

        let selectionModal = document.getElementById('memberSelectionModal') as HTMLDivElement;
        selectionModal.classList.remove('hidden');
        showModal();
      })
      .catch((error) => {
        displayCreateSecretCollectionError('fetch users', error)
      })

  }
}

function moveSelectedOptions(source: HTMLSelectElement | HTMLDataListElement, target: HTMLSelectElement | HTMLDataListElement, elementName: string): void {
  for (let optionRaw of Array.from(source.children)) {
    let option = optionRaw as HTMLOptionElement;
    if (!option.selected && (elementName === '' || option.value !== elementName )) {
      continue;
    }
    target.appendChild(option.cloneNode(true));
    source.removeChild(option);
  }
}

function optionWithValue(value: string): HTMLOptionElement {
  let option = document.createElement('option') as HTMLOptionElement;
  option.value = value;
  option.innerText = value;
  return option;
}

// getSelectValues is a helper to get all options in a select
function getSelectValues(select: HTMLSelectElement): string[] {
  let result: string[] = [];
  for (let childRaw of Array.from(select.children)) {
    const child = childRaw as HTMLOptionElement;
    result.push(child.value);
  }
  return result;
}

function deleteColectionEventHandler(collectionName: string) {
  return function () {
    const deleteConfirmation = document.getElementById('deleteConfirmation') as HTMLDivElement;
    // clear div before adding elements, this avoids innerHTML issues
    while(deleteConfirmation.firstChild) {
      deleteConfirmation.removeChild(deleteConfirmation.firstChild);
    }
    deleteConfirmation.appendChild(document.createTextNode(`Are you sure you want to irreversibly delete the secret collection ${collectionName} and all its content?`));
    deleteConfirmation.appendChild(document.createElement('br'));
    deleteConfirmation.appendChild(document.createElement('br'));

    const cancelButton = document.createElement('button') as HTMLButtonElement;
    cancelButton.type = 'button';
    cancelButton.innerHTML = 'cancel';
    cancelButton.classList.add('grey-button');
    cancelButton.addEventListener('click', () => hideModal());
    deleteConfirmation.appendChild(cancelButton);

    const confirmButton = document.createElement('button') as HTMLButtonElement;
    confirmButton.type = 'button';
    confirmButton.innerHTML = '<i class="fa fa-trash"></i> Delete';
    confirmButton.classList.add('red-button');
    confirmButton.addEventListener('click', () => {
      fetch(`${window.location.protocol}//${window.location.host}/secretcollection/${collectionName}`, { method: 'DELETE' })
        .then(async (response) => {
          if (!response.ok) {
            const msg = await response.text();
            throw msg;
          }
          fetchAndRenderSecretCollections();
          hideModal();
        })
        .catch((error) => {
          displayCreateSecretCollectionError('delete secret collection', error);
        });
    });
    deleteConfirmation.append('          ');
    deleteConfirmation.appendChild(confirmButton);

    clearCreateSecretCollectionError();
    document.getElementById('deleteConfirmation')?.classList.remove('hidden');
    showModal();
  };
}

function renderCollectionTable(data: secretCollection[]) {
  if (data === null) {
    // The generated javascript loop will do a NPD if this is null because it accesses the .length property
    data = new Array<secretCollection>();
  }
  const newTableBody = document.createElement('tbody') as HTMLTableSectionElement;
  newTableBody.id = 'secretCollectionTableBody';
  for (const secretCollection of data) {
    const row = newTableBody.insertRow();
    row.insertCell().innerText = secretCollection.name;
    row.insertCell().innerText = secretCollection.path;
    row.insertCell().innerText = secretCollection.members.toString();

    const buttonCell = row.insertCell();
    let editMembersButton = document.createElement('button') as HTMLButtonElement;
    editMembersButton.classList.add('blue-button');
    editMembersButton.innerHTML = 'Edit Members';
    const editMembersHandler = editMembersEventHandler(secretCollection);
    editMembersButton.addEventListener('click', () => editMembersHandler());
    buttonCell.appendChild(editMembersButton);
    buttonCell.append(' ');

    let deleteButton = document.createElement('button') as HTMLButtonElement;
    deleteButton.classList.add('red-button');
    deleteButton.innerHTML = '<i class="fa fa-trash"></i> Delete';
    const deleteHandler = deleteColectionEventHandler(secretCollection.name);
    deleteButton.addEventListener('click', () => deleteHandler());
    buttonCell.appendChild(deleteButton);
  }

  const oldTableBody = document.getElementById('secretCollectionTableBody') as HTMLTableSectionElement;
  oldTableBody.parentNode?.replaceChild(newTableBody, oldTableBody);
}

function fetchAndRenderSecretCollections() {
  fetch(`${window.location.protocol}//${window.location.host}/secretcollection`)
    .then(async (response) => {
      const msg = await response.text();
      if (!response.ok) {
        throw msg;
      }
      let secretCollections = [] as secretCollection[];
      if (msg !== '') {
        secretCollections = JSON.parse(msg) as secretCollection[];
      }
      renderCollectionTable(secretCollections);
    });
}

function createSecretCollection() {
  const input = document.getElementById('newSecretCollectionName') as HTMLInputElement;
  const name = input.value;
  input.value = '';
  fetch(`${window.location.protocol}//${window.location.host}/secretcollection/${name}`, { method: 'PUT' })
    .then(async (response) => {
      if (!response.ok) {
        const responseText = await response.text();
        throw responseText;
      }
      fetchAndRenderSecretCollections();
      hideModal();
    })
    .catch((error) => {
      displayCreateSecretCollectionError('create secret collection', error);
    });
}

document.getElementById('newCollectionButton')?.addEventListener('click', () => {
  document.getElementById('createCollectionInput')?.classList.remove('hidden');
  showModal();
  document.getElementById('newSecretCollectionName')?.focus();
});

document.getElementById('abortCreateCollectionButton')?.addEventListener('click', () => hideModal());
document.getElementById('updateMemberCancelButton')?.addEventListener('click', () => hideModal());

document.addEventListener('keydown', (event) => {
  const escKeyCode = 27;
  const enterKeyCode = 13;
  if (event.keyCode === escKeyCode) {
    hideModal();
  } else if (event.keyCode === enterKeyCode && !document.getElementById('createCollectionInput')?.classList.contains('hidden')) {
    createSecretCollection();
  }
});

document.getElementById('createCollectionButton').addEventListener('click', () => createSecretCollection());


const secretCollections: secretCollection[] = JSON.parse(document.getElementById('secretcollections').innerHTML);
renderCollectionTable(secretCollections);
