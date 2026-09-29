<template>
  <div class="invoice_email">
    <h3>InvoiceEmail</h3>
    <p>Статус: {{ status }}</p>
  </div>
</template>

<script>
// InvoiceEmailPanel — панель «счёт»: отправляет счёт клиенту по email.
export default {
  name: "InvoiceEmailPanel",
  data() {
    return { processed: 0, skipped: 0 };
  },
  computed: {
    // status отображает итог обработки.
    status() {
      return `обработано: ${this.processed}, пропущено: ${this.skipped}`;
    },
  },
  methods: {
    // refresh запускает обработку (счёт).
    async refresh() {
      this.processed = 0;
      this.skipped = 0;
    },
  },
};
</script>

<style scoped>
.invoice_email { padding: 0.5rem; }
</style>
