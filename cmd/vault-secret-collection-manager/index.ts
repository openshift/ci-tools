interface secretCollection {
  name:    string;
  path:    string;
  members: string[];
}

const secretCollections: secretCollection[] = JSON.parse(document.getElementById('secretcollections').innerHTML);

function renderCollectionTable(data: secretCollection[]) {
  const newTableBody = document.createElement("tbody") as HTMLTableSectionElement;
  newTableBody.id = "secretCollectionTableBody";
  for (let secretCollection of data){
    const row = newTableBody.insertRow();
    row.insertCell().innerHTML = secretCollection.name;
    row.insertCell().innerHTML = secretCollection.path;
    row.insertCell().innerHTML = secretCollection.members.toString();
  }

  const oldTableBody = document.getElementById("secretCollectionTableBody") as HTMLTableSectionElement;
  oldTableBody.parentNode?.replaceChild(newTableBody, oldTableBody);
}
renderCollectionTable(secretCollections);

function createSecretCollection(){
  let input = document.getElementById("name") as HTMLInputElement;
  let name = input.value;
  input.value = "";
  fetch(window.location.protocol + "//" + window.location.host + "/secretcollection/" + name, {method: "PUT"})
  .then(function (response) {
    if (response.ok) {
      fetchAndRenderSecretCollections();
      (document.getElementById("createCollection") as HTMLDivElement).classList.add("hidden");
    } else {
      return response.text();
    };
  })
  .then(function (errMsg: string){
    displayCreateSecretCollectionError(errMsg)  ;
  })
  .catch(function (error) {
    displayCreateSecretCollectionError(error);
  });
};

function fetchAndRenderSecretCollections() {
  fetch(window.location.protocol + "//" + window.location.host + "/secretcollection/")
  .then(function(response) {
    if (response.ok) {
      return response.text();
    }
  })
  .then(function(data: string) {
    renderCollectionTable(JSON.parse(data) as secretCollection[]);
  });
}

function displayCreateSecretCollectionError(msg: string) {
  let div = document.getElementById("createCollectionError") as HTMLDivElement;
  div.innerHTML = `Failed to create secret colltion: ${msg}`;
  div.classList.remove("hidden");
}

function clearCreateSecretCollectionError(){
  let div = document.getElementById("createCollectionError") as HTMLDivElement;
  div.innerHTML = ""
  div.classList.add("hidden");
}

document.getElementById("newCollectionButton")?.addEventListener("click", (e: Event) => {
  clearCreateSecretCollectionError();
  document.getElementById("createCollection")?.classList.remove("hidden");
})
document.getElementById("abortCreateCollectionButton")?.addEventListener("click", (e: Event) => {
  document.getElementById("createCollection")?.classList.add("hidden");
})
document.addEventListener("keydown", event => {
  // esc
  if (event.keyCode == 27) {
    (document.getElementById("createCollection") as HTMLDivElement).classList.add("hidden");
  };
})

document.getElementById("createCollectionButton").addEventListener("click", (e: Event) => createSecretCollection());
