<template>
  <div class="payment_retry">
    <h3>PaymentRetry</h3>
    <p>Статус: {{ status }}</p>
  </div>
</template>

<script>
// PaymentRetryPanel — панель «оплата»: повторяет неудачную оплату.
export default {
  name: "PaymentRetryPanel",
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
    // refresh запускает обработку (оплата).
    async refresh() {
      this.processed = 0;
      this.skipped = 0;
    },
  },
};
</script>

<style scoped>
.payment_retry { padding: 0.5rem; }
</style>
